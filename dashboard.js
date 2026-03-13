'use strict';

const PLATFORM_COLORS = { youtube: 'youtube', tiktok: 'tiktok', linkedin: 'linkedin' };
const PLATFORM_LABELS = { youtube: 'YouTube', tiktok: 'TikTok', linkedin: 'LinkedIn' };
const PLATFORM_ORDER  = ['youtube', 'tiktok', 'linkedin'];
const RANGE_LABELS    = { last7: 'Last 7 days', last30: 'Last 30 days', last6mo: 'Last 6 months', last12mo: 'Last 12 months', ytd: 'Year to date', all: 'All time' };

function rangeCutoff(range) {
  if (range === 'last7')   { const d = new Date(); d.setDate(d.getDate() - 7);   d.setHours(0,0,0,0); return d; }
  if (range === 'last30')  { const d = new Date(); d.setDate(d.getDate() - 30);  d.setHours(0,0,0,0); return d; }
  if (range === 'last6mo') { const n = new Date(); return new Date(n.getFullYear(), n.getMonth() - 6, n.getDate()); }
  if (range === 'last12mo'){ const n = new Date(); return new Date(n.getFullYear() - 1, n.getMonth(), n.getDate()); }
  if (range === 'ytd')     { return new Date(new Date().getFullYear(), 0, 1); }
  return null;
}

let currentReport    = null;
let previousReport   = null;   // immediately prior report in index (for NEW / % badge)
let prevViewMap      = {};     // platform:videoId → views from previousReport
let allReportEntries = [];
let chartInstance      = null;
let trendChartInstance = null;
let wowChartInstance   = null;
let trendGrouping      = 'month'; // 'month' (time scale) | 'date' (ordinal)

// ── Local Mode Detection ──────────────────────────────────────────────────────

function isLocalMode() {
  const proto = window.location.protocol;
  const host  = window.location.hostname;
  return proto === 'file:' || host === 'localhost' || host === '127.0.0.1';
}

// ── Data Loading (fetch + file:// fallback via script tags) ──────────────────

async function loadData(jsonPath) {
  try {
    const res = await fetch(jsonPath);
    if (res.ok) return res.json();
  } catch (_) { /* fall through */ }

  const jsPath    = jsonPath.replace(/\.json$/, '.js');
  const globalKey = jsonPath.includes('index') ? '__devrelIndex' : '__devrelReport';

  return new Promise((resolve, reject) => {
    delete window[globalKey];
    document.querySelectorAll(`script[data-devrel="${jsPath}"]`).forEach(s => s.remove());
    const script = document.createElement('script');
    script.src = jsPath;
    script.dataset.devrel = jsPath;
    script.onload = () => {
      if (window[globalKey] !== undefined) resolve(window[globalKey]);
      else reject(new Error(`${jsPath} loaded but data not set`));
    };
    script.onerror = () => reject(new Error(`Could not load ${jsPath} — run the fetch script first`));
    document.head.appendChild(script);
  });
}

// ── Formatting ────────────────────────────────────────────────────────────────

function fmt(n) {
  if (n === null || n === undefined) return '0';
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1).replace(/\.0$/, '') + 'M';
  if (n >= 1_000)     return (n / 1_000).toFixed(1).replace(/\.0$/, '') + 'K';
  return n.toLocaleString();
}

function fmtDuration(s) {
  if (!s) return '';
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;
}

function fmtDate(iso) {
  if (!iso) return '';
  return new Date(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' });
}

function fmtDateShort(iso) {
  if (!iso) return '';
  return new Date(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}

// ── Growth Helpers ────────────────────────────────────────────────────────────

// Returns {start, end} for the equivalent PREVIOUS period, or null for 'all'.
function previousRangeBounds(range) {
  const now = new Date();
  if (range === 'last7') {
    const end = new Date(now); end.setDate(end.getDate() - 7); end.setHours(0,0,0,0);
    const start = new Date(end); start.setDate(start.getDate() - 7);
    return { start, end };
  }
  if (range === 'last30') {
    const end = new Date(now); end.setDate(end.getDate() - 30); end.setHours(0,0,0,0);
    const start = new Date(end); start.setDate(start.getDate() - 30);
    return { start, end };
  }
  if (range === 'ytd') {
    const start = new Date(now.getFullYear() - 1, 0, 1);
    const end   = new Date(now.getFullYear() - 1, now.getMonth(), now.getDate());
    return { start, end };
  }
  if (range === 'last6mo') {
    const end   = new Date(now.getFullYear(), now.getMonth() - 6, now.getDate());
    const start = new Date(now.getFullYear(), now.getMonth() - 12, now.getDate());
    return { start, end };
  }
  if (range === 'last12mo') {
    const end   = new Date(now.getFullYear() - 1, now.getMonth(), now.getDate());
    const start = new Date(now.getFullYear() - 2, now.getMonth(), now.getDate());
    return { start, end };
  }
  return null; // 'all', 'custom' — no comparison
}

function filterItemsByBounds(items, start, end) {
  return items.filter(i => {
    if (!i.publishedAt) return false;
    const d = new Date(i.publishedAt);
    return d >= start && d < end;
  });
}

function sumTotals(items) {
  const t = { total: 0, youtube: 0, tiktok: 0, linkedin: 0 };
  for (const item of items) {
    t.total += item.totalViews;
    for (const p of item.platforms) {
      if (p.platform in t) t[p.platform] += p.views || 0;
    }
  }
  return t;
}

// Returns % change, or null if prev is 0/null.
function pctChange(curr, prev) {
  if (prev == null || prev === 0) return null;
  return (curr - prev) / prev * 100;
}

function growthEl(pct) {
  if (pct === null || pct === undefined) return null;
  const rounded = Math.round(pct);
  if (rounded === 0) return null;
  const span = document.createElement('span');
  span.className = 'growth-badge ' + (rounded >= 0 ? 'up' : 'down');
  span.textContent = (rounded >= 0 ? '+' : '') + rounded + '%';
  return span;
}

function newBadge() {
  const span = document.createElement('span');
  span.className = 'growth-badge new';
  span.textContent = 'NEW';
  return span;
}

function isWithin7Days(dateStr) {
  if (!dateStr) return false;
  return Date.now() - new Date(dateStr).getTime() < 7 * 86400 * 1000;
}

// ── Previous Report View Map ──────────────────────────────────────────────────

function buildPrevViewMap(report) {
  if (!report) return {};
  const map = {};
  for (const group of (report.video_groups || [])) {
    for (const [platform, data] of Object.entries(group.platforms || {})) {
      map[`${platform}:${data.video_id}`] = data.views;
    }
  }
  for (const v of (report.unmatched || [])) {
    map[`${v.platform}:${v.video_id}`] = v.views;
  }
  return map;
}

// ── Unified List Builder ──────────────────────────────────────────────────────

function buildUnifiedList(report) {
  const items = [];

  for (const group of (report.video_groups || [])) {
    const publishedDates = Object.values(group.platforms || {})
      .map(p => p.published_at).filter(Boolean).sort();
    const publishedAt = publishedDates[0] || null;

    const platforms = Object.entries(group.platforms || {})
      .sort(([a], [b]) => (PLATFORM_ORDER.indexOf(a) + 1 || 99) - (PLATFORM_ORDER.indexOf(b) + 1 || 99))
      .map(([platform, data]) => ({ platform, ...data }));

    items.push({
      canonicalTitle:  group.canonical_title || '(untitled)',
      totalViews:      group.total_views || 0,
      durationSeconds: group.duration_seconds || 0,
      publishedAt,
      platforms,
      videoIds: platforms.map(p => ({ platform: p.platform, id: p.video_id })),
      cardKey:  'group:' + group.id,
    });
  }

  for (const v of (report.unmatched || [])) {
    items.push({
      canonicalTitle:  v.title || '(untitled)',
      totalViews:      v.views || 0,
      durationSeconds: v.duration_seconds || 0,
      publishedAt:     v.published_at || null,
      platforms: [{
        platform:        v.platform,
        video_id:        v.video_id,
        title:           v.title,
        views:           v.views,
        url:             v.url,
        published_at:    v.published_at,
        duration_seconds: v.duration_seconds,
      }],
      videoIds: [{ platform: v.platform, id: v.video_id }],
      cardKey:  'unmatched:' + v.platform + ':' + v.video_id,
    });
  }

  items.sort((a, b) => {
    if (!a.publishedAt && !b.publishedAt) return 0;
    if (!a.publishedAt) return 1;
    if (!b.publishedAt) return -1;
    return new Date(b.publishedAt) - new Date(a.publishedAt);
  });

  return items;
}

// ── Filtering ─────────────────────────────────────────────────────────────────

function filterItems(items, range) {
  if (range === 'custom') {
    const from = getParam('from');
    const to   = getParam('to');
    if (!from && !to) return items;
    const start = from ? new Date(from) : null;
    const end   = to   ? new Date(to + 'T23:59:59') : null;
    return items.filter(item => {
      if (!item.publishedAt) return false;
      const d = new Date(item.publishedAt);
      return (!start || d >= start) && (!end || d <= end);
    });
  }
  const cutoff = rangeCutoff(range);
  if (!cutoff) return items;
  return items.filter(item => item.publishedAt && new Date(item.publishedAt) >= cutoff);
}

// ── Rolling 12-Month Stats + Trend Chart ─────────────────────────────────────

function buildRolling12MonthData(allItems) {
  const now    = new Date();
  const cutoff = new Date(now.getFullYear(), now.getMonth() - 11, 1);

  // Filter to 12-month window, sort oldest → newest by publish date
  const relevant = allItems
    .filter(item => item.publishedAt && new Date(item.publishedAt) >= cutoff)
    .sort((a, b) => new Date(a.publishedAt) - new Date(b.publishedAt));

  // Group by calendar day (YYYY-MM-DD) so same-day releases share one data point
  const byDate = new Map();
  for (const item of relevant) {
    const key = item.publishedAt.slice(0, 10);
    if (!byDate.has(key)) byDate.set(key, { total: 0, yt: 0, tt: 0, li: 0 });
    const b = byDate.get(key);
    b.total += item.totalViews;
    for (const p of item.platforms) {
      const v = p.views || 0;
      if (p.platform === 'youtube')  b.yt += v;
      if (p.platform === 'tiktok')   b.tt += v;
      if (p.platform === 'linkedin') b.li += v;
    }
  }

  // Build both ordinal (labels + value arrays) and time-scale ({x,y} arrays) in one pass.
  // Ordinal = one label per data point, spaced evenly regardless of time.
  // Time    = {x: date, y: value} objects; Chart.js positions points by actual date.
  const labels = [], totals = [], yt = [], tt = [], li = [];
  const tData  = [], ytData = [], ttData = [], liData = [];
  let cumTotal = 0, cumYt = 0, cumTt = 0, cumLi = 0;

  for (let m = 0; m < 12; m++) {
    const slotDate  = new Date(cutoff.getFullYear(), cutoff.getMonth() + m, 1);
    const slotYear  = slotDate.getFullYear();
    const slotMonth = slotDate.getMonth();

    const datesInMonth = [...byDate.keys()]
      .filter(key => {
        const d = new Date(key + 'T12:00:00');
        return d.getFullYear() === slotYear && d.getMonth() === slotMonth;
      })
      .sort();

    if (datesInMonth.length > 0) {
      for (const key of datesInMonth) {
        const b = byDate.get(key);
        cumTotal += b.total; cumYt += b.yt; cumTt += b.tt; cumLi += b.li;
        const d = new Date(key + 'T12:00:00');
        labels.push(d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' }));
        totals.push(cumTotal); yt.push(cumYt); tt.push(cumTt); li.push(cumLi);
        tData.push({ x: key, y: cumTotal });
        ytData.push({ x: key, y: cumYt });
        ttData.push({ x: key, y: cumTt });
        liData.push({ x: key, y: cumLi });
      }
    } else {
      // No releases this month — hold the line with a point at the 1st
      const iso = slotDate.toISOString().slice(0, 10);
      labels.push(slotDate.toLocaleDateString(undefined, { month: 'short', day: 'numeric' }));
      totals.push(cumTotal); yt.push(cumYt); tt.push(cumTt); li.push(cumLi);
      tData.push({ x: iso, y: cumTotal });
      ytData.push({ x: iso, y: cumYt });
      ttData.push({ x: iso, y: cumTt });
      liData.push({ x: iso, y: cumLi });
    }
  }

  return { labels, totals, yt, tt, li, tData, ytData, ttData, liData, sums: { total: cumTotal, yt: cumYt, tt: cumTt, li: cumLi } };
}

function renderRolling(allItems) {
  const data = buildRolling12MonthData(allItems);
  const ids  = ['rolling-total', 'rolling-yt', 'rolling-tt', 'rolling-li'];
  const vals = [data.sums.total, data.sums.yt, data.sums.tt, data.sums.li];
  ids.forEach((id, i) => { const el = document.getElementById(id); if (el) el.textContent = fmt(vals[i]); });
  if (!trendChartInstance) initTrendChart(data);
  else updateTrendChart(data);
}


function buildTrendChartData(data, grouping) {
  const useTime = grouping === 'month';
  const common  = { cubicInterpolationMode: 'monotone', borderWidth: 2, pointRadius: 3 };
  const chartData = {
    datasets: [
      { label: 'Total',    data: useTime ? data.tData  : data.totals, borderColor: 'rgba(108,99,255,0.9)',   backgroundColor: 'rgba(108,99,255,0.08)', fill: true,  ...common },
      { label: 'YouTube',  data: useTime ? data.ytData : data.yt,     borderColor: 'rgba(255,51,51,0.85)',   backgroundColor: 'transparent',           fill: false, ...common },
      { label: 'TikTok',   data: useTime ? data.ttData : data.tt,     borderColor: 'rgba(105,201,208,0.85)', backgroundColor: 'transparent',           fill: false, ...common },
      { label: 'LinkedIn', data: useTime ? data.liData : data.li,     borderColor: 'rgba(10,102,194,0.85)',  backgroundColor: 'transparent',           fill: false, ...common },
    ],
  };
  if (!useTime) chartData.labels = data.labels;
  return chartData;
}

function initTrendChart(data) {
  if (!window.Chart) return;
  const canvas = document.getElementById('trend-chart');
  if (!canvas) return;
  Chart.defaults.color = '#7c83a0';
  const xScale = trendGrouping === 'month'
    ? { type: 'time', time: { unit: 'month', displayFormats: { month: 'MMM yy' } }, grid: { color: '#2e3250' }, ticks: { font: { size: 11 }, maxRotation: 0 } }
    : { grid: { color: '#2e3250' }, ticks: { font: { size: 11 }, maxTicksLimit: 12, maxRotation: 0 } };

  trendChartInstance = new Chart(canvas, {
    type: 'line',
    data: buildTrendChartData(data, trendGrouping),
    options: {
      responsive: true, maintainAspectRatio: false,
      interaction: { mode: 'index', intersect: false },
      plugins: {
        legend: { labels: { boxWidth: 12, font: { size: 12 } } },
        tooltip: { callbacks: { label: ctx => ` ${ctx.dataset.label}: ${fmt(ctx.parsed.y)}` } },
      },
      scales: {
        x: xScale,
        y: { beginAtZero: true, grid: { color: '#2e3250' }, ticks: { callback: v => fmt(v), font: { size: 11 } } },
      },
    },
  });
}

function updateTrendChart(data) {
  if (!trendChartInstance) return;
  const d = buildTrendChartData(data, trendGrouping);
  trendChartInstance.data.labels   = d.labels;
  trendChartInstance.data.datasets = d.datasets;
  trendChartInstance.update('none');
}

// ── Week-over-Week Chart ──────────────────────────────────────────────────────

function buildWeekOverWeekData(allItems, numWeeks = 12) {
  // Align to Monday-based weeks
  const startOfMonday = d => {
    const day = d.getDay(); // 0=Sun
    const diff = day === 0 ? -6 : 1 - day;
    const mon = new Date(d);
    mon.setDate(d.getDate() + diff);
    mon.setHours(0, 0, 0, 0);
    return mon;
  };

  const thisWeek = startOfMonday(new Date());
  const cutoff   = new Date(thisWeek);
  cutoff.setDate(cutoff.getDate() - (numWeeks - 1) * 7);

  // Build week slots
  const slots = Array.from({ length: numWeeks }, (_, i) => {
    const start = new Date(cutoff);
    start.setDate(cutoff.getDate() + i * 7);
    const end = new Date(start);
    end.setDate(start.getDate() + 7);
    return { start, end, yt: 0, tt: 0, li: 0 };
  });

  for (const item of allItems) {
    if (!item.publishedAt) continue;
    const d = new Date(item.publishedAt);
    if (d < cutoff) continue;
    for (const slot of slots) {
      if (d >= slot.start && d < slot.end) {
        for (const p of item.platforms) {
          const v = p.views || 0;
          if (p.platform === 'youtube')  slot.yt += v;
          if (p.platform === 'tiktok')   slot.tt += v;
          if (p.platform === 'linkedin') slot.li += v;
        }
        break;
      }
    }
  }

  const labels = slots.map(s =>
    s.start.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
  );
  return { labels, yt: slots.map(s => s.yt), tt: slots.map(s => s.tt), li: slots.map(s => s.li) };
}

function initWowChart(data) {
  if (!window.Chart) return;
  const canvas = document.getElementById('wow-chart');
  if (!canvas) return;
  wowChartInstance = new Chart(canvas, {
    type: 'bar',
    data: {
      labels: data.labels,
      datasets: [
        { label: 'YouTube',  data: data.yt, backgroundColor: 'rgba(255,51,51,0.75)',   stack: 'views' },
        { label: 'TikTok',   data: data.tt, backgroundColor: 'rgba(105,201,208,0.75)', stack: 'views' },
        { label: 'LinkedIn', data: data.li, backgroundColor: 'rgba(10,102,194,0.75)',  stack: 'views' },
      ],
    },
    options: {
      responsive: true, maintainAspectRatio: false,
      interaction: { mode: 'index', intersect: false },
      plugins: {
        legend: { labels: { boxWidth: 12, font: { size: 12 } } },
        tooltip: {
          callbacks: {
            footer: items => ' Total: ' + fmt(items.reduce((s, i) => s + i.parsed.y, 0)),
            label:  ctx  => ` ${ctx.dataset.label}: ${fmt(ctx.parsed.y)}`,
          },
        },
      },
      scales: {
        x: { stacked: true, grid: { color: '#2e3250' }, ticks: { font: { size: 11 }, maxRotation: 0 } },
        y: { stacked: true, beginAtZero: true, grid: { color: '#2e3250' }, ticks: { callback: v => fmt(v), font: { size: 11 } } },
      },
    },
  });
}

function renderWow(allItems) {
  const data = buildWeekOverWeekData(allItems);
  if (!wowChartInstance) {
    initWowChart(data);
  } else {
    wowChartInstance.data.labels = data.labels;
    wowChartInstance.data.datasets[0].data = data.yt;
    wowChartInstance.data.datasets[1].data = data.tt;
    wowChartInstance.data.datasets[2].data = data.li;
    wowChartInstance.update('none');
  }
}

// ── Summary ───────────────────────────────────────────────────────────────────

function renderSummary(currItems, prevItems) {
  const curr = sumTotals(currItems);
  const prev = prevItems ? sumTotals(prevItems) : null;

  const pairs = [
    { id: 'total-views', curr: curr.total,    prev: prev?.total    },
    { id: 'yt-views',    curr: curr.youtube,  prev: prev?.youtube  },
    { id: 'tt-views',    curr: curr.tiktok,   prev: prev?.tiktok   },
    { id: 'li-views',    curr: curr.linkedin, prev: prev?.linkedin },
  ];

  for (const { id, curr: c, prev: p } of pairs) {
    const valueEl = document.getElementById(id);
    if (!valueEl) continue;
    valueEl.textContent = fmt(c);

    const card = valueEl.closest('.stat-card');
    const existing = card?.querySelector('.growth-badge');
    if (existing) existing.remove();

    const badge = growthEl(pctChange(c, p));
    if (badge && card) card.appendChild(badge);
  }

  return { curr, prev };
}

// ── Chart ─────────────────────────────────────────────────────────────────────

function initChart() {
  if (!window.Chart) return;
  const canvas = document.getElementById('views-chart');
  if (!canvas) return;

  Chart.defaults.color = '#7c83a0';

  chartInstance = new Chart(canvas, {
    type: 'bar',
    data: { labels: ['YouTube', 'TikTok', 'LinkedIn'], datasets: [] },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      interaction: { mode: 'index' },
      plugins: {
        legend: { labels: { boxWidth: 12, font: { size: 12 } } },
        tooltip: {
          callbacks: {
            label: ctx => ` ${ctx.dataset.label}: ${fmt(ctx.raw)}`,
          },
        },
      },
      scales: {
        x: { grid: { color: '#2e3250' }, ticks: { font: { size: 12 } } },
        y: { beginAtZero: true, grid: { color: '#2e3250' },
             ticks: { callback: v => fmt(v), font: { size: 12 } } },
      },
    },
  });
}

function updateChart(curr, prev) {
  if (!chartInstance) return;

  // Labels = bar groups. One group per platform so each bar shows current vs previous stacked.
  // Layout: X-axis = [YouTube, TikTok, LinkedIn]
  // Dataset per "series" where each dataset has one value per bar group.
  // We use Chart.js stack groups so current and previous sit side-by-side (not stacked on each other).

  const YT  = 'rgba(255,51,51,0.85)';
  const TT  = 'rgba(105,201,208,0.85)';
  const LI  = 'rgba(10,102,194,0.85)';
  const YTf = 'rgba(255,51,51,0.35)';
  const TTf = 'rgba(105,201,208,0.35)';
  const LIf = 'rgba(10,102,194,0.35)';

  // Grouped bar: each platform is a bar group, each group has up to 2 bars (curr / prev)
  // We model this as: datasets = platforms, labels = ['Current', 'Previous']
  //   OR labels = platforms, datasets = [Current, Previous]
  // The latter is easier to read.

  const datasets = [
    {
      label: 'Current period',
      data: [curr.youtube, curr.tiktok, curr.linkedin],
      backgroundColor: [YT, TT, LI],
      borderRadius: 4,
    },
  ];

  if (prev) {
    datasets.push({
      label: 'Previous period',
      data: [prev.youtube, prev.tiktok, prev.linkedin],
      backgroundColor: [YTf, TTf, LIf],
      borderRadius: 4,
    });
  }

  chartInstance.data.datasets = datasets;
  chartInstance.update('none');
}

// ── Merge Selection ───────────────────────────────────────────────────────────
// Supports selecting whole cards OR individual platform rows.
// selectionItems maps selectKey → { videoIds, label }

const selectionItems = new Map();
let   renderedItems  = [];

// ── Hidden Videos ─────────────────────────────────────────────────────────────

let hiddenVideos = new Set(JSON.parse(localStorage.getItem('devrel-hidden-videos') || '[]'));

function saveHiddenVideos() {
  localStorage.setItem('devrel-hidden-videos', JSON.stringify([...hiddenVideos]));
}

function updateHiddenBar(count) {
  const bar   = document.getElementById('hidden-bar');
  const label = document.getElementById('hidden-bar-label');
  if (!bar) return;
  bar.hidden = count === 0;
  if (label) label.textContent = `${count} video${count !== 1 ? 's' : ''} hidden`;
}

function clearHiddenVideos() {
  hiddenVideos.clear();
  saveHiddenVideos();
  if (currentReport) renderReport(currentReport, getActiveRange());
}

function toggleItemSelection(key, videoIds, label, el, btn) {
  if (selectionItems.has(key)) {
    selectionItems.delete(key);
    el.classList.remove('selected');
    if (btn) btn.textContent = 'Select';
  } else {
    if (selectionItems.size >= 2) return;
    selectionItems.set(key, { videoIds, label });
    el.classList.add('selected');
    if (btn) btn.textContent = 'Deselect';
  }
  updateMergeBar();
}

function clearSelection() {
  selectionItems.clear();
  document.querySelectorAll('.video-card.selected, .platform-version-row.selected').forEach(el => {
    el.classList.remove('selected');
  });
  document.querySelectorAll('.select-btn').forEach(btn => { btn.textContent = 'Select all'; });
  document.querySelectorAll('.pv-select-btn').forEach(btn => { btn.textContent = 'Select'; });
  updateMergeBar();
}

function updateMergeBar() {
  const bar = document.getElementById('merge-bar');
  if (!bar) return;
  const count = selectionItems.size;
  bar.hidden = count === 0;
  const label = bar.querySelector('.merge-bar-label');
  if (label) {
    label.textContent = count === 1
      ? '1 selected — pick one more to merge'
      : '2 selected — ready to merge';
  }
  const btn = bar.querySelector('.merge-btn');
  if (btn) btn.disabled = count < 2;
}

async function doMerge() {
  if (selectionItems.size < 2) return;

  const allVideoIds = [...selectionItems.values()].flatMap(i => i.videoIds);
  const note        = [...selectionItems.values()].map(i => i.label).join(' ↔ ');
  const newEntry    = { note, video_ids: allVideoIds };

  let existing = [];
  try {
    const res = await fetch('manual_groups.json');
    if (res.ok) existing = await res.json();
  } catch (_) {}

  const updated = [...existing, newEntry];
  const content = JSON.stringify(updated, null, 2) + '\n';

  // Native save-file dialog (Chrome / Edge) — saves in place and reloads
  if (window.showSaveFilePicker) {
    try {
      const handle = await window.showSaveFilePicker({
        suggestedName: 'manual_groups.json',
        types: [{ description: 'JSON file', accept: { 'application/json': ['.json'] } }],
      });
      const writable = await handle.createWritable();
      await writable.write(content);
      await writable.close();
      clearSelection();
      sessionStorage.setItem('mergeNotice', '1');
      location.reload();
      return;
    } catch (err) {
      if (err.name === 'AbortError') return; // user cancelled the dialog
      // Fall through to blob download
    }
  }

  // Fallback: blob download (Firefox / Safari / file:// without FSAPI)
  const blob = new Blob([content], { type: 'application/json' });
  const url  = URL.createObjectURL(blob);
  const a    = document.createElement('a');
  a.href     = url;
  a.download = 'manual_groups.json';
  a.click();
  URL.revokeObjectURL(url);
  clearSelection();
  showNotice('manual_groups.json downloaded. Replace the file in your project folder, then re-run <code>go run ./cmd/fetch --skip-linkedin</code> to apply it.');
}

// ── Notice Bar ────────────────────────────────────────────────────────────────

function showNotice(msg) {
  const bar = document.createElement('div');
  bar.className = 'notice-bar';
  bar.innerHTML = `<span>${msg}</span><button class="notice-close" onclick="this.parentElement.remove()">×</button>`;
  document.querySelector('main').prepend(bar);
}

// ── Card Rendering ────────────────────────────────────────────────────────────

function renderCard(item) {
  const card = document.createElement('div');
  card.className = 'video-card';

  // Header: title + total views + duration
  const header = document.createElement('div');
  header.className = 'video-card-header';

  const titleEl = document.createElement('div');
  titleEl.className = 'video-title';
  titleEl.textContent = item.canonicalTitle;

  const meta = document.createElement('div');
  meta.className = 'video-meta';

  const totalEl = document.createElement('div');
  totalEl.className = 'video-total-views';
  totalEl.textContent = fmt(item.totalViews);

  // NEW badge if published < 7 days ago; otherwise growth % if meaningful
  const prevTotal  = item.videoIds.reduce((sum, v) => sum + (prevViewMap[`${v.platform}:${v.id}`] || 0), 0);
  const totalBadge = isWithin7Days(item.publishedAt)
    ? newBadge()
    : growthEl(pctChange(item.totalViews, prevTotal || null));
  if (totalBadge) totalEl.appendChild(totalBadge);

  const durationEl = document.createElement('div');
  durationEl.className = 'video-duration';
  if (item.durationSeconds) durationEl.textContent = fmtDuration(item.durationSeconds);

  meta.append(totalEl, durationEl);
  header.append(titleEl, meta);

  // Publish date
  const dateEl = document.createElement('div');
  dateEl.className = 'video-date';
  dateEl.textContent = item.publishedAt ? fmtDate(item.publishedAt) : '';

  // Platform version rows
  const local = isLocalMode();
  const versions = document.createElement('div');
  versions.className = local ? 'platform-versions local' : 'platform-versions';

  for (const p of item.platforms) {
    // Use a div so we can put a <button> inside without invalid HTML nesting
    const row = document.createElement('div');
    row.className = 'platform-version-row';
    row.dataset.href = p.url || '';

    // Clicking the row (but not the button) opens the link
    row.addEventListener('click', (e) => {
      if (e.target.closest('.pv-select-btn')) return;
      if (p.url) window.open(p.url, '_blank', 'noopener,noreferrer');
    });

    const dot = document.createElement('span');
    dot.className = `platform-dot ${PLATFORM_COLORS[p.platform] || 'unknown'}`;

    const nameEl = document.createElement('span');
    nameEl.className = 'pv-name';
    nameEl.textContent = PLATFORM_LABELS[p.platform] || p.platform;

    const pvTitle = document.createElement('span');
    pvTitle.className = 'pv-title';
    pvTitle.textContent = p.title || '';

    const pvDate = document.createElement('span');
    pvDate.className = 'pv-date';
    pvDate.textContent = p.published_at ? fmtDateShort(p.published_at) : '';

    const pvViews = document.createElement('span');
    pvViews.className = 'pv-views';
    pvViews.textContent = fmt(p.views);

    // NEW badge if published < 7 days ago; otherwise growth % if meaningful
    const prevPlatformViews = prevViewMap[`${p.platform}:${p.video_id}`] ?? null;
    const pvBadge = isWithin7Days(p.published_at)
      ? newBadge()
      : growthEl(pctChange(p.views || 0, prevPlatformViews));
    if (pvBadge) pvViews.appendChild(pvBadge);

    row.append(dot, nameEl, pvTitle, pvDate, pvViews);

    // Per-row select button (local mode only)
    if (local) {
      const pvBtn = document.createElement('button');
      pvBtn.className   = 'pv-select-btn';
      pvBtn.textContent = 'Select';
      const rowKey = `pvrow:${p.platform}:${p.video_id}`;
      const rowLabel = `${PLATFORM_LABELS[p.platform] || p.platform} – ${p.title || '(untitled)'}`;
      pvBtn.addEventListener('click', (e) => {
        e.stopPropagation();
        toggleItemSelection(rowKey, [{ platform: p.platform, id: p.video_id }], rowLabel, row, pvBtn);
      });
      row.appendChild(pvBtn);
    }

    versions.appendChild(row);
  }

  card.append(header, dateEl, versions);

  // Hide button
  const hideBtn = document.createElement('button');
  hideBtn.className   = 'hide-btn';
  hideBtn.textContent = '×';
  hideBtn.title       = 'Hide this video';
  hideBtn.addEventListener('click', (e) => {
    e.stopPropagation();
    hiddenVideos.add(item.cardKey);
    saveHiddenVideos();
    if (currentReport) renderReport(currentReport, getActiveRange());
  });
  card.appendChild(hideBtn);

  // Card-level select button (local mode only) — selects all platform versions at once
  if (local) {
    const btn = document.createElement('button');
    btn.className   = 'select-btn';
    btn.textContent = 'Select all';
    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      toggleItemSelection(item.cardKey, item.videoIds, item.canonicalTitle, card, btn);
    });
    card.appendChild(btn);
  }

  return card;
}

// ── Video List ────────────────────────────────────────────────────────────────

function renderVideoList(items, range) {
  renderedItems = items;
  const container = document.getElementById('video-groups');
  const heading   = document.querySelector('.section-heading');
  container.innerHTML = '';
  clearSelection();

  const visible      = items.filter(item => !hiddenVideos.has(item.cardKey));
  const hiddenCount  = items.length - visible.length;
  updateHiddenBar(hiddenCount);

  if (heading) {
    heading.textContent = visible.length > 0 ? `Videos (${visible.length})` : 'Videos';
  }

  if (!visible.length) {
    const cutoff = rangeCutoff(range);
    const since  = cutoff ? ` since ${fmtDate(cutoff.toISOString())}` : '';
    const msg    = hiddenCount > 0 && items.length > 0
      ? 'All videos in this range are hidden.'
      : `No videos${since}.`;
    container.innerHTML = `<p class="state-message">${msg}</p>`;
    return;
  }

  for (const item of visible) {
    container.appendChild(renderCard(item));
  }
}

// ── Selected Heading ──────────────────────────────────────────────────────────

function updateSelectedHeading(range) {
  const el = document.getElementById('selected-heading');
  if (!el) return;
  let label;
  if (range === 'custom') {
    const from = getParam('from');
    const to   = getParam('to');
    if (from && to)  label = `${fmtDate(from)} – ${fmtDate(to)}`;
    else if (from)   label = `From ${fmtDate(from)}`;
    else if (to)     label = `Until ${fmtDate(to)}`;
    else             label = 'Custom range';
  } else {
    label = RANGE_LABELS[range] || 'All time';
  }
  el.textContent = `Selected Time Totals (${label})`;
}

// ── Report Render ─────────────────────────────────────────────────────────────

function renderReport(report, range) {
  updateSelectedHeading(range);

  const updatedEl = document.getElementById('last-updated');
  if (updatedEl && report.generated_at) {
    updatedEl.textContent = 'Updated ' + fmtDate(report.generated_at);
  }

  const allItems  = buildUnifiedList(report);
  renderRolling(allItems);
  renderWow(allItems);
  const currItems = filterItems(allItems, range);

  // Previous equivalent period (date-shifted, same report) — for stat cards + chart
  let prevItems = null;
  const bounds = previousRangeBounds(range);
  if (bounds) prevItems = filterItemsByBounds(allItems, bounds.start, bounds.end);

  const { curr, prev } = renderSummary(currItems, prevItems);
  updateChart(curr, prev);
  renderVideoList(currItems, range);
}

// ── Report Selector ───────────────────────────────────────────────────────────

function renderReportSelector(entries, currentID) {
  const select = document.getElementById('report-select');
  select.innerHTML = '';
  for (const entry of entries) {
    const opt = document.createElement('option');
    opt.value = entry.id;
    opt.textContent = new Date(entry.generated_at).toLocaleString(undefined, {
      month: 'short', day: 'numeric', year: 'numeric', hour: '2-digit', minute: '2-digit',
    });
    if (entry.id === currentID) opt.selected = true;
    select.appendChild(opt);
  }
  select.addEventListener('change', () => {
    setParams({ report: select.value });
    loadAndRender(select.value);
  });
}

// ── Query String ──────────────────────────────────────────────────────────────

function getParam(key) {
  return new URLSearchParams(window.location.search).get(key);
}

function setParams(updates) {
  const url = new URL(window.location.href);
  for (const [k, v] of Object.entries(updates)) {
    if (v == null) url.searchParams.delete(k);
    else url.searchParams.set(k, v);
  }
  window.history.pushState({}, '', url.toString());
}

function getActiveRange() {
  return getParam('range') || 'all';
}

// ── Range Tabs ────────────────────────────────────────────────────────────────

function syncRangeTabs(range) {
  document.querySelectorAll('.range-tab').forEach(t => {
    t.classList.toggle('active', t.dataset.range === range);
  });
}

function initRangeTabs() {
  const range         = getActiveRange();
  const customRangeEl = document.getElementById('custom-range');
  const fromInput     = document.getElementById('custom-from');
  const toInput       = document.getElementById('custom-to');

  syncRangeTabs(range);
  if (customRangeEl) customRangeEl.hidden = range !== 'custom';
  if (fromInput && getParam('from')) fromInput.value = getParam('from');
  if (toInput   && getParam('to'))   toInput.value   = getParam('to');

  function applyCustom() {
    const from = fromInput?.value || null;
    const to   = toInput?.value   || null;
    setParams({ range: 'custom', from, to });
    updateSelectedHeading('custom');
    if (currentReport) renderReport(currentReport, 'custom');
  }
  if (fromInput) fromInput.addEventListener('change', applyCustom);
  if (toInput)   toInput.addEventListener('change', applyCustom);

  document.querySelectorAll('.range-tab').forEach(tab => {
    tab.addEventListener('click', () => {
      const r = tab.dataset.range;
      syncRangeTabs(r);
      if (customRangeEl) customRangeEl.hidden = r !== 'custom';
      if (r === 'custom') {
        setParams({ range: 'custom', from: fromInput?.value || null, to: toInput?.value || null });
      } else {
        setParams({ range: r === 'all' ? null : r, from: null, to: null });
      }
      if (currentReport) renderReport(currentReport, r);
    });
  });
}

// ── Trend Toggle ──────────────────────────────────────────────────────────────

function initTrendToggle() {
  document.querySelectorAll('.trend-toggle-btn').forEach(btn => {
    btn.addEventListener('click', () => {
      const grouping = btn.dataset.grouping;
      if (grouping === trendGrouping) return;
      trendGrouping = grouping;
      document.querySelectorAll('.trend-toggle-btn').forEach(b => {
        b.classList.toggle('active', b.dataset.grouping === grouping);
      });
      // Scale type change requires destroying and recreating the chart
      if (trendChartInstance) { trendChartInstance.destroy(); trendChartInstance = null; }
      if (currentReport) renderRolling(buildUnifiedList(currentReport));
    });
  });
}

// ── Merge Bar ─────────────────────────────────────────────────────────────────

function initMergeBar() {
  const bar = document.getElementById('merge-bar');
  if (!bar) return;
  if (!isLocalMode()) { bar.remove(); return; }

  bar.querySelector('.merge-cancel-btn').addEventListener('click', clearSelection);
  bar.querySelector('.merge-btn').addEventListener('click', doMerge);
}

// ── Export to Image ───────────────────────────────────────────────────────────

function initExportButton() {
  const btn = document.getElementById('export-btn');
  if (!btn) return;
  btn.addEventListener('click', async () => {
    if (!window.html2canvas) return;
    document.body.classList.add('exporting');
    try {
      const canvas = await html2canvas(document.body, {
        backgroundColor: '#0f1117',
        scale: 2,
        useCORS: true,
        logging: false,
      });
      const a = document.createElement('a');
      a.href = canvas.toDataURL('image/png');
      a.download = 'devrel-dashboard.png';
      a.click();
    } finally {
      document.body.classList.remove('exporting');
    }
  });
}

// ── Main ──────────────────────────────────────────────────────────────────────

async function loadAndRender(reportID) {
  document.getElementById('video-groups').innerHTML = '<p class="state-message">Loading…</p>';
  try {
    // Load current + previous reports in parallel
    const currentIdx = allReportEntries.findIndex(e => e.id === reportID);
    const prevEntry  = currentIdx >= 0 && currentIdx < allReportEntries.length - 1
      ? allReportEntries[currentIdx + 1]
      : null;

    const [loaded, loadedPrev] = await Promise.all([
      loadData(`reports/${reportID}.json`),
      prevEntry ? loadData(`reports/${prevEntry.id}.json`).catch(() => null) : Promise.resolve(null),
    ]);

    currentReport  = loaded;
    previousReport = loadedPrev;
    prevViewMap    = buildPrevViewMap(previousReport);

    renderReport(currentReport, getActiveRange());
  } catch (err) {
    document.getElementById('video-groups').innerHTML =
      `<p class="state-message">Failed to load report: ${err.message}</p>`;
  }
}

async function init() {
  initRangeTabs();
  initTrendToggle();
  initMergeBar();
  initChart();
  initExportButton();

  const hiddenShowBtn = document.getElementById('hidden-bar-show');
  if (hiddenShowBtn) hiddenShowBtn.addEventListener('click', clearHiddenVideos);

  if (sessionStorage.getItem('mergeNotice')) {
    sessionStorage.removeItem('mergeNotice');
    showNotice('Manual group saved. Re-run <code>go run ./cmd/fetch --skip-linkedin</code> to apply it to your next report.');
  }

  let index;
  try {
    index = await loadData('reports/index.json');
  } catch {
    document.getElementById('video-groups').innerHTML =
      '<p class="state-message">No reports found. Run: <code>go run ./cmd/fetch --skip-linkedin</code></p>';
    return;
  }

  allReportEntries = index.reports || [];
  if (!allReportEntries.length) {
    document.getElementById('video-groups').innerHTML =
      '<p class="state-message">No reports yet. Run: <code>go run ./cmd/fetch --skip-linkedin</code></p>';
    return;
  }

  const paramID   = getParam('report');
  const currentID = allReportEntries.find(e => e.id === paramID) ? paramID : allReportEntries[0].id;
  renderReportSelector(allReportEntries, currentID);
  await loadAndRender(currentID);
}

window.addEventListener('DOMContentLoaded', init);

window.addEventListener('popstate', () => {
  const range         = getActiveRange();
  const customRangeEl = document.getElementById('custom-range');
  const fromInput     = document.getElementById('custom-from');
  const toInput       = document.getElementById('custom-to');
  syncRangeTabs(range);
  if (customRangeEl) customRangeEl.hidden = range !== 'custom';
  if (fromInput) fromInput.value = getParam('from') || '';
  if (toInput)   toInput.value   = getParam('to')   || '';
  const paramID = getParam('report');
  if (paramID) loadAndRender(paramID);
  else if (currentReport) renderReport(currentReport, range);
});
