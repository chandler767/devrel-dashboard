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
let trendGrouping      = 'date'; // 'month' (time scale) | 'date' (ordinal)
let decayCurve         = null;  // { youtube, tiktok, linkedin } — power-law α per platform
let showWowPred        = true;  // whether projection bars are visible in WoW chart
let activeMetric       = 'views'; // 'views' | 'likes' | 'comments' | 'shares'
let transcriptStore    = {};    // "platform:videoId" → { text, source, ... }
let currentAnalysis    = null;  // loaded analysis result for the current report

const METRIC_LABELS = { views: 'Views', likes: 'Likes', comments: 'Comments', shares: 'Shares' };

// Which engagement fields each platform actually provides.
const PLATFORM_CAPS = {
  youtube:  { views: true, likes: true, comments: true, shares: false, clicks: false, commentTexts: true  },
  tiktok:   { views: true, likes: true, comments: true, shares: true,  clicks: false, commentTexts: false },
  linkedin: { views: true, likes: true, comments: true, shares: true,  clicks: true,  commentTexts: false },
};

function metricVal(item, metric) {
  switch (metric) {
    case 'likes':    return item.totalLikes    || 0;
    case 'comments': return item.totalComments || 0;
    case 'shares':   return item.totalShares   || 0;
    default:         return item.totalViews    || 0;
  }
}

function platformMetricVal(p, metric) {
  switch (metric) {
    case 'likes':    return p.likes    || 0;
    case 'comments': return p.comments || 0;
    case 'shares':   return p.shares   || 0;
    default:         return p.views    || 0;
  }
}

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
  const globalKey = jsonPath.includes('transcripts') ? '__devrelTranscripts'
                  : jsonPath.includes('analysis/')    ? '__devrelAnalysis'
                  : jsonPath.includes('index')        ? '__devrelIndex'
                  : '__devrelReport';

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
  return new Date(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric', timeZone: 'UTC' });
}

function fmtDateShort(iso) {
  if (!iso) return '';
  return new Date(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric', timeZone: 'UTC' });
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

function sumTotals(items, metric = 'views') {
  const t = { total: 0, youtube: 0, tiktok: 0, linkedin: 0 };
  for (const item of items) {
    t.total += metricVal(item, metric);
    for (const p of item.platforms) {
      if (p.platform in t) t[p.platform] += platformMetricVal(p, metric);
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

    const groupTranscripts = {};
    for (const p of platforms) {
      const entry = transcriptStore[`${p.platform}:${p.video_id}`];
      if (entry?.text) groupTranscripts[p.platform] = entry.text;
    }

    items.push({
      canonicalTitle:  group.canonical_title || '(untitled)',
      totalViews:      group.total_views    || 0,
      totalLikes:      group.total_likes    || 0,
      totalComments:   group.total_comments || 0,
      totalShares:     group.total_shares   || 0,
      thumbnail:       group.thumbnail      || null,
      description:     group.description    || '',
      tags:            group.tags           || [],
      durationSeconds: group.duration_seconds || 0,
      publishedAt,
      platforms,
      transcripts: groupTranscripts,
      videoIds: platforms.map(p => ({ platform: p.platform, id: p.video_id })),
      cardKey:  'group:' + group.id,
    });
  }

  for (const v of (report.unmatched || [])) {
    items.push({
      canonicalTitle:  v.title || '(untitled)',
      totalViews:      v.views    || 0,
      totalLikes:      v.likes    || 0,
      totalComments:   v.comments || 0,
      totalShares:     v.shares   || 0,
      thumbnail:       v.thumbnail    || null,
      description:     v.description  || '',
      tags:            v.tags         || [],
      durationSeconds: v.duration_seconds || 0,
      publishedAt:     v.published_at || null,
      platforms: [{
        platform:        v.platform,
        video_id:        v.video_id,
        title:           v.title,
        views:           v.views    || 0,
        likes:           v.likes    || 0,
        comments:        v.comments || 0,
        shares:          v.shares   || 0,
        clicks:          v.clicks   || 0,
        comment_texts:   v.comment_texts || [],
        thumbnail:       v.thumbnail    || null,
        description:     v.description  || '',
        tags:            v.tags         || [],
        url:             v.url,
        published_at:    v.published_at,
        duration_seconds: v.duration_seconds,
      }],
      transcripts: (() => {
        const t = {};
        const entry = transcriptStore[`${v.platform}:${v.video_id}`];
        if (entry?.text) t[v.platform] = entry.text;
        return t;
      })(),
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

function buildRolling12MonthData(allItems, metric = 'views') {
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
    b.total += metricVal(item, metric);
    for (const p of item.platforms) {
      const v = platformMetricVal(p, metric);
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

function renderRolling(allItems, metric = activeMetric) {
  const data = buildRolling12MonthData(allItems, metric);
  const ids  = ['rolling-total', 'rolling-yt', 'rolling-tt', 'rolling-li'];
  const vals = [data.sums.total, data.sums.yt, data.sums.tt, data.sums.li];
  ids.forEach((id, i) => { const el = document.getElementById(id); if (el) el.textContent = fmt(vals[i]); });
  // Update rolling section heading to reflect active metric
  const rollingHeading = document.getElementById('rolling-metric-label');
  if (rollingHeading) rollingHeading.textContent = METRIC_LABELS[metric] || 'Views';
  // Hide rolling stat cards for platforms that don't support this metric
  [['rolling-yt', 'youtube'], ['rolling-tt', 'tiktok'], ['rolling-li', 'linkedin']].forEach(([id, plat]) => {
    const card = document.getElementById(id)?.closest('.stat-card');
    if (card) card.hidden = !(PLATFORM_CAPS[plat]?.[metric] ?? true);
  });
  if (!trendChartInstance) initTrendChart(data);
  else updateTrendChart(data);
  // Hide trend chart datasets for unsupported platforms (indices: 1=YT, 2=TT, 3=LI)
  if (trendChartInstance) {
    [['youtube', 1], ['tiktok', 2], ['linkedin', 3]].forEach(([plat, idx]) => {
      trendChartInstance.setDatasetVisibility(idx, PLATFORM_CAPS[plat]?.[metric] ?? true);
    });
    trendChartInstance.update('none');
  }
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
        legend: {
          labels: {
            boxWidth: 12, font: { size: 12 },
            filter: item => {
              const platMap = { YouTube: 'youtube', TikTok: 'tiktok', LinkedIn: 'linkedin' };
              const plat = platMap[item.text];
              return plat ? (PLATFORM_CAPS[plat]?.[activeMetric] ?? true) : true;
            },
          },
        },
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

// ── Linear Regression ─────────────────────────────────────────────────────────

function linearRegression(values) {
  const n = values.length;
  if (n < 2) return values.slice();
  let sumX = 0, sumY = 0, sumXY = 0, sumX2 = 0;
  for (let i = 0; i < n; i++) {
    sumX += i; sumY += values[i]; sumXY += i * values[i]; sumX2 += i * i;
  }
  const denom = n * sumX2 - sumX * sumX;
  if (denom === 0) return values.slice();
  const slope     = (n * sumXY - sumX * sumY) / denom;
  const intercept = (sumY - slope * sumX) / n;
  return Array.from({ length: n }, (_, i) => Math.max(0, Math.round(slope * i + intercept)));
}

// ── Week-over-Week Chart ──────────────────────────────────────────────────────

function buildWeekOverWeekData(allItems, numWeeks = 12, reportDate, metric = 'views') {
  // Align to Monday-based weeks — use UTC throughout so publish dates like
  // "2026-04-06T00:00:00Z" (UTC Monday) don't shift into Sunday in local time.
  const startOfMonday = d => {
    const day = d.getUTCDay(); // 0=Sun
    const diff = day === 0 ? -6 : 1 - day;
    const mon = new Date(d);
    mon.setUTCDate(d.getUTCDate() + diff);
    mon.setUTCHours(0, 0, 0, 0);
    return mon;
  };

  // Anchor to the report date so the rightmost column is always the report's week
  const anchor   = reportDate ? new Date(reportDate) : new Date();
  const now      = anchor.getTime();
  const msPerDay = 86400000;
  const thisWeek = startOfMonday(anchor);
  const cutoff   = new Date(thisWeek);
  cutoff.setUTCDate(cutoff.getUTCDate() - (numWeeks - 1) * 7);

  // Build week slots — each slot also stores per-platform projItems for immature videos
  const slots = Array.from({ length: numWeeks }, (_, i) => {
    const start = new Date(cutoff);
    start.setUTCDate(cutoff.getUTCDate() + i * 7);
    const end = new Date(start);
    end.setUTCDate(start.getUTCDate() + 7);
    return {
      start, end, yt: 0, tt: 0, li: 0,
      projItems: { youtube: [], tiktok: [], linkedin: [] },
    };
  });

  for (const item of allItems) {
    if (!item.publishedAt) continue;
    const pub     = new Date(item.publishedAt);
    const ageDays = (now - pub.getTime()) / msPerDay;

    // Slot assignment + collect projItems for videos < 30 days old (projections only apply to views)
    if (pub >= cutoff) {
      for (const slot of slots) {
        if (pub >= slot.start && pub < slot.end) {
          for (const p of item.platforms) {
            const v = platformMetricVal(p, metric);
            if (p.platform === 'youtube')  slot.yt += v;
            if (p.platform === 'tiktok')   slot.tt += v;
            if (p.platform === 'linkedin') slot.li += v;
            if (metric === 'views' && ageDays < 30 && slot.projItems[p.platform]) {
              slot.projItems[p.platform].push({ views: p.views || 0, ageDays });
            }
          }
          break;
        }
      }
    }
  }

  const labels = slots.map(s =>
    s.start.toLocaleDateString(undefined, { month: 'short', day: 'numeric', timeZone: 'UTC' })
  );
  const ytArr = slots.map(s => s.yt);
  const ttArr = slots.map(s => s.tt);
  const liArr = slots.map(s => s.li);
  const totals = ytArr.map((v, i) => v + ttArr[i] + liArr[i]);
  const trend = linearRegression(totals);
  const trendSlope = trend.length >= 2
    ? Math.round((trend[trend.length - 1] - trend[0]) / (trend.length - 1))
    : 0;

  // Projections only apply to views
  if (metric !== 'views') {
    return {
      labels, yt: ytArr, tt: ttArr, li: liArr,
      predYt: slots.map(() => null), predTt: slots.map(() => null), predLi: slots.map(() => null),
      hasPred: false, trend, trendSlope,
    };
  }

  // Compute per-slot projections using the empirical power-law decay curve.
  // All slots project uniformly to day 30 so bars are directly comparable.
  // For a video at age D with V views: projected_additional = V * ((30/D)^α - 1)
  for (const slot of slots) {
    for (const [plat, items] of Object.entries(slot.projItems)) {
      if (items.length === 0) continue;
      const α = (decayCurve?.[plat]) ?? 0.35;
      let additional = 0;
      for (const v of items) {
        if (v.views <= 0 || v.ageDays <= 0) continue;
        additional += v.views * (Math.pow(30 / v.ageDays, α) - 1);
      }
      slot['pred_' + plat] = Math.round(additional);
    }
  }

  const predYt = slots.map(s => s.pred_youtube || null);
  const predTt = slots.map(s => s.pred_tiktok  || null);
  const predLi = slots.map(s => s.pred_linkedin || null);
  const hasPred = predYt.some(v => v) || predTt.some(v => v) || predLi.some(v => v);

  return {
    labels, yt: ytArr, tt: ttArr, li: liArr,
    predYt, predTt, predLi, hasPred, trend, trendSlope,
  };
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
        { label: 'YouTube',            data: data.yt,     backgroundColor: 'rgba(255,51,51,0.75)',   stack: 'views' },
        { label: 'TikTok',             data: data.tt,     backgroundColor: 'rgba(105,201,208,0.75)', stack: 'views' },
        { label: 'LinkedIn',           data: data.li,     backgroundColor: 'rgba(10,102,194,0.75)',  stack: 'views' },
        { label: 'YouTube (proj.)',    data: data.predYt, backgroundColor: 'rgba(255,51,51,0.25)',   borderColor: 'rgba(255,51,51,0.6)',   borderWidth: 1, borderDash: [4,3], stack: 'views', pointStyle: false },
        { label: 'TikTok (proj.)',     data: data.predTt, backgroundColor: 'rgba(105,201,208,0.25)', borderColor: 'rgba(105,201,208,0.6)', borderWidth: 1, borderDash: [4,3], stack: 'views', pointStyle: false },
        { label: 'LinkedIn (proj.)',   data: data.predLi, backgroundColor: 'rgba(10,102,194,0.25)',  borderColor: 'rgba(10,102,194,0.6)',  borderWidth: 1, borderDash: [4,3], stack: 'views', pointStyle: false },
        { label: 'Trend',              data: data.trend,  type: 'line', borderColor: 'rgba(255,255,255,0.5)', borderWidth: 2, borderDash: [6,3], pointRadius: 0, pointHoverRadius: 0, pointHitRadius: 0, fill: false, tension: 0 },
      ],
    },
    options: {
      responsive: true, maintainAspectRatio: false,
      interaction: { mode: 'index', intersect: true },
      plugins: {
        legend: {
          labels: {
            boxWidth: 12, font: { size: 12 },
            filter: item => {
              if (item.text.includes('proj.')) return false;
              if (item.text === 'Trend') return activeMetric !== 'views' || showWowPred;
              const platMap = { YouTube: 'youtube', TikTok: 'tiktok', LinkedIn: 'linkedin' };
              const plat = platMap[item.text];
              return plat ? (PLATFORM_CAPS[plat]?.[activeMetric] ?? true) : true;
            },
          },
          onClick: (e, legendItem, legend) => {
            if (legendItem.datasetIndex === 6) return; // Trend is controlled by predictions toggle
            Chart.defaults.plugins.legend.onClick.call(legend.chart, e, legendItem, legend);
            applyWowPredVisibility();
            updateWowTrend();
          },
        },
        tooltip: {
          filter: item => item.dataset.label !== 'Trend',
          callbacks: {
            label: ctx => {
              const v = ctx.parsed.y;
              if (v == null || v === 0) return null;
              const proj = ctx.dataset.label.includes('proj.');
              const name = ctx.dataset.label.replace(' (proj.)', '');
              return proj
                ? ` ${name} +${fmt(v)} est.`
                : ` ${name}: ${fmt(v)}`;
            },
            footer: items => {
              const isActual = i => !i.dataset.label.includes('proj.') && i.dataset.label !== 'Trend';
              const actual = items.filter(isActual).reduce((s, i) => s + (i.parsed.y || 0), 0);
              const projExtra = items.filter(i => i.dataset.label.includes('proj.')).reduce((s, i) => s + (i.parsed.y || 0), 0);
              if (projExtra > 0) {
                return [` Actual: ${fmt(actual)}`, ` Proj. total: ${fmt(actual + projExtra)}  (3-mo avg rate)`];
              }
              return ` Total: ${fmt(actual)}`;
            },
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

function applyWowPredVisibility() {
  if (!wowChartInstance) return;
  // Proj. datasets (3=YT, 4=TT, 5=LI) mirror their platform dataset visibility
  [3, 4, 5].forEach(i => {
    const platVisible = wowChartInstance.isDatasetVisible(i - 3);
    wowChartInstance.setDatasetVisibility(i, showWowPred && platVisible);
  });
  // Trend line: always visible for non-views; for views follows predictions toggle
  wowChartInstance.setDatasetVisibility(6, activeMetric !== 'views' || showWowPred);
  wowChartInstance.update('none');
  const statEl = document.getElementById('wow-trend-stat');
  if (statEl) statEl.hidden = activeMetric === 'views' && !showWowPred;
}

function updateWowTrend() {
  if (!wowChartInstance) return;
  const chart  = wowChartInstance;
  const n      = chart.data.labels.length;
  const totals = Array.from({ length: n }, (_, i) => {
    let sum = 0;
    for (const di of [0, 1, 2]) {
      if (chart.isDatasetVisible(di)) sum += chart.data.datasets[di].data[i] || 0;
    }
    return sum;
  });
  const trend = linearRegression(totals);
  const slope = trend.length >= 2
    ? Math.round((trend[trend.length - 1] - trend[0]) / (trend.length - 1))
    : 0;
  chart.data.datasets[6].data = trend;
  chart.update('none');
  const statEl = document.getElementById('wow-trend-stat');
  if (statEl) {
    statEl.hidden = false;
    statEl.textContent = 'Trend  ' + (slope >= 0 ? '+' : '') + fmt(slope) + ' additional ' + (METRIC_LABELS[activeMetric] || 'views').toLowerCase() + ' / wk';
    statEl.className   = 'wow-trend-stat ' + (slope >= 0 ? 'positive' : 'negative');
  }
}

function renderWow(allItems, reportDate, metric = activeMetric) {
  const data = buildWeekOverWeekData(allItems, 12, reportDate, metric);
  // Predictions toggle only makes sense for views
  const predBtn = document.getElementById('wow-pred-btn');
  if (predBtn) predBtn.hidden = metric !== 'views';
  // Update subheading to reflect active metric
  const subEl = document.getElementById('wow-subheading');
  if (subEl) {
    const label = METRIC_LABELS[metric]?.toLowerCase() || metric;
    subEl.textContent = `Total accumulated ${label} on videos published each week — not new ${label} gained that week`;
  }
  const isNew = !wowChartInstance;
  if (isNew) {
    initWowChart(data);
    const btn = document.getElementById('wow-pred-btn');
    if (btn) {
      btn.addEventListener('click', () => {
        showWowPred = !showWowPred;
        btn.classList.toggle('active', showWowPred);
        btn.textContent = showWowPred ? 'Predictions: on' : 'Predictions: off';
        applyWowPredVisibility();
      });
    }
  } else {
    wowChartInstance.data.labels           = data.labels;
    wowChartInstance.data.datasets[0].data = data.yt;
    wowChartInstance.data.datasets[1].data = data.tt;
    wowChartInstance.data.datasets[2].data = data.li;
    wowChartInstance.data.datasets[3].data = data.predYt;
    wowChartInstance.data.datasets[4].data = data.predTt;
    wowChartInstance.data.datasets[5].data = data.predLi;
    wowChartInstance.data.datasets[6].data = data.trend;
    wowChartInstance.update('none');
  }
  // Hide WoW datasets for platforms that don't support this metric
  // Indices: 0=YT, 1=TT, 2=LI, 3=YT pred, 4=TT pred, 5=LI pred, 6=Trend
  if (wowChartInstance) {
    [['youtube', 0], ['tiktok', 1], ['linkedin', 2]].forEach(([plat, idx]) => {
      wowChartInstance.setDatasetVisibility(idx, PLATFORM_CAPS[plat]?.[metric] ?? true);
    });
    wowChartInstance.update('none');
  }
  applyWowPredVisibility();
  updateWowTrend();
}

// ── Summary ───────────────────────────────────────────────────────────────────

function renderSummary(currItems, prevItems, metric = activeMetric) {
  const curr = sumTotals(currItems, metric);
  const prev = prevItems ? sumTotals(prevItems, metric) : null;

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

  // Hide summary stat cards for platforms that don't support this metric
  [['yt-views', 'youtube'], ['tt-views', 'tiktok'], ['li-views', 'linkedin']].forEach(([id, plat]) => {
    const card = document.getElementById(id)?.closest('.stat-card');
    if (card) card.hidden = !(PLATFORM_CAPS[plat]?.[metric] ?? true);
  });

  // Update summary section label
  const summaryMetricLabel = document.getElementById('summary-metric-label');
  if (summaryMetricLabel) summaryMetricLabel.textContent = METRIC_LABELS[metric] || 'Views';

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

  const YT  = 'rgba(255,51,51,0.85)';
  const TT  = 'rgba(105,201,208,0.85)';
  const LI  = 'rgba(10,102,194,0.85)';
  const YTf = 'rgba(255,51,51,0.35)';
  const TTf = 'rgba(105,201,208,0.35)';
  const LIf = 'rgba(10,102,194,0.35)';

  // Only include platforms that support the active metric
  const allPlatforms = [
    { key: 'youtube',  label: 'YouTube',  currVal: curr.youtube,  prevVal: prev?.youtube,  color: YT,  colorFade: YTf },
    { key: 'tiktok',   label: 'TikTok',   currVal: curr.tiktok,   prevVal: prev?.tiktok,   color: TT,  colorFade: TTf },
    { key: 'linkedin', label: 'LinkedIn', currVal: curr.linkedin, prevVal: prev?.linkedin, color: LI,  colorFade: LIf },
  ];
  const visible = allPlatforms.filter(p => PLATFORM_CAPS[p.key]?.[activeMetric] ?? true);

  chartInstance.data.labels = visible.map(p => p.label);
  const datasets = [
    {
      label: 'Current period',
      data: visible.map(p => p.currVal),
      backgroundColor: visible.map(p => p.color),
      borderRadius: 4,
    },
  ];
  if (prev) {
    datasets.push({
      label: 'Previous period',
      data: visible.map(p => p.prevVal),
      backgroundColor: visible.map(p => p.colorFade),
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

// ── Report Polling ────────────────────────────────────────────────────────────

function startReportPolling() {
  const INTERVAL_MS = 5 * 60 * 1000; // 5 minutes
  let notified = false;

  const check = async () => {
    if (notified) return;
    try {
      const res = await fetch('reports/index.json?_t=' + Date.now(), { cache: 'no-cache' });
      if (!res.ok) return;
      const data = await res.json();
      const newest  = (data.reports || [])[0];
      const current = allReportEntries[0];
      if (newest && current && newest.id !== current.id) {
        notified = true;
        showUpdateNotice('A new report is available.', 'Reload', () => location.reload());
      }
    } catch (_) {}
  };

  setInterval(check, INTERVAL_MS);
}

function showUpdateNotice(msg, actionLabel, actionFn) {
  if (document.querySelector('.update-notice')) return; // don't stack
  const bar = document.createElement('div');
  bar.className = 'notice-bar update-notice';
  const btn = document.createElement('button');
  btn.className = 'notice-btn';
  btn.textContent = actionLabel;
  btn.addEventListener('click', actionFn);
  bar.innerHTML =
    `<span>${msg}</span>` +
    '<div style="display:flex;gap:8px;align-items:center;flex-shrink:0;"></div>';
  bar.querySelector('div').append(btn);
  const close = document.createElement('button');
  close.className = 'notice-close';
  close.textContent = '×';
  close.addEventListener('click', () => bar.remove());
  bar.querySelector('div').append(close);
  document.querySelector('main').prepend(bar);
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


  const header = document.createElement('div');
  header.className = 'video-card-header';

  const meta = document.createElement('div');
  meta.className = 'video-meta';

  // Views + engagement all on one line in the header
  const supportedPlatforms = item.platforms.map(p => p.platform);

  const totalEl = document.createElement('span');
  totalEl.className = 'video-total-views';
  totalEl.textContent = `▶ ${fmt(item.totalViews)}`;
  meta.append(totalEl);

  const engDefs = [
    { key: 'likes',    icon: '♥',  val: item.totalLikes    },
    { key: 'comments', icon: '◉',  val: item.totalComments },
    { key: 'shares',   icon: '⬆',  val: item.totalShares   },
  ];
  for (const def of engDefs) {
    const supported = supportedPlatforms.some(pl => (PLATFORM_CAPS[pl] || {})[def.key]);
    if (!supported) continue;
    if (def.key === 'comments') {
      const btn = document.createElement('button');
      btn.className = 'pv-comment-btn eng-stat';
      btn.textContent = `${def.icon} ${fmt(def.val)}`;
      // scope to first platform that has comment texts
      const commentPlatform = item.platforms.find(p => PLATFORM_CAPS[p.platform]?.commentTexts) || item.platforms[0];
      btn.addEventListener('click', (e) => { e.stopPropagation(); showCommentsModal(item, commentPlatform); });
      meta.append(btn);
    } else {
      const span = document.createElement('span');
      span.className = 'eng-stat';
      span.textContent = `${def.icon} ${fmt(def.val)}`;
      meta.append(span);
    }
  }

  header.append(meta);

  // Tags
  let tagsEl = null;
  if (item.tags && item.tags.length > 0) {
    tagsEl = document.createElement('div');
    tagsEl.className = 'video-tags';
    const displayTags = item.tags.slice(0, 8);
    for (const tag of displayTags) {
      const chip = document.createElement('span');
      chip.className = 'tag-chip';
      chip.textContent = '#' + tag;
      tagsEl.appendChild(chip);
    }
  }


  // Platform version rows
  const local = isLocalMode();
  const versions = document.createElement('div');
  versions.className = local ? 'platform-versions local' : 'platform-versions';

  for (const p of item.platforms) {
    const row = document.createElement('div');
    row.className = 'platform-version-row';
    row.dataset.href = p.url || '';

    row.addEventListener('click', (e) => {
      if (e.target.closest('.pv-select-btn')) return;
      if (e.target.closest('.pv-engagement')) return;
      if (p.url) window.open(p.url, '_blank', 'noopener,noreferrer');
    });

    const dot = document.createElement('span');
    dot.className = `platform-dot ${PLATFORM_COLORS[p.platform] || 'unknown'}`;

    const nameEl = document.createElement('span');
    nameEl.className = 'pv-name';
    nameEl.textContent = PLATFORM_LABELS[p.platform] || p.platform;

    const pvTitle = document.createElement('span');
    pvTitle.className = 'pv-title';
    // NEW badge goes at the start of the title cell, before the title text
    if (isWithin7Days(p.published_at)) pvTitle.appendChild(newBadge());
    pvTitle.appendChild(document.createTextNode(p.title || ''));

    const pvViews = document.createElement('span');
    pvViews.className = 'pv-views';
    pvViews.textContent = `▶ ${fmt(p.views)}`;
    const prevPlatformViews = prevViewMap[`${p.platform}:${p.video_id}`] ?? null;
    if (!isWithin7Days(p.published_at)) {
      const pvGrowth = growthEl(pctChange(p.views || 0, prevPlatformViews));
      if (pvGrowth) pvViews.appendChild(pvGrowth);
    }

    const caps = PLATFORM_CAPS[p.platform] || {};
    const pvEng = document.createElement('span');
    pvEng.className = 'pv-engagement';
    if (caps.likes)    pvEng.appendChild(Object.assign(document.createElement('span'), { textContent: `♥ ${fmt(p.likes || 0)}` }));
    if (caps.comments) {
      const pvCommentBtn = document.createElement('button');
      pvCommentBtn.className = 'pv-comment-btn';
      pvCommentBtn.textContent = `◉ ${fmt(p.comments || 0)}`;
      pvCommentBtn.addEventListener('click', (e) => { e.stopPropagation(); showCommentsModal(item, p); });
      pvEng.appendChild(pvCommentBtn);
    }
    if (caps.shares) pvEng.appendChild(Object.assign(document.createElement('span'), { textContent: `⬆ ${fmt(p.shares || 0)}` }));

    row.append(dot, nameEl, pvTitle, pvViews, pvEng);

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

  card.append(header);
  if (tagsEl) card.appendChild(tagsEl);
  card.appendChild(versions);

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

function renderDayCard(dayItems) {
  const card = document.createElement('div');
  card.className = 'video-card';

  // Header: date on left, aggregated totals on right
  const header = document.createElement('div');
  header.className = 'video-card-header';

  const dateLabel = document.createElement('span');
  dateLabel.className = 'day-card-date';
  dateLabel.textContent = dayItems[0]?.publishedAt ? fmtDate(dayItems[0].publishedAt) : 'Unknown date';

  const meta = document.createElement('div');
  meta.className = 'video-meta';

  const totalViews    = dayItems.reduce((s, i) => s + (i.totalViews    || 0), 0);
  const totalLikes    = dayItems.reduce((s, i) => s + (i.totalLikes    || 0), 0);
  const totalComments = dayItems.reduce((s, i) => s + (i.totalComments || 0), 0);
  const totalShares   = dayItems.reduce((s, i) => s + (i.totalShares   || 0), 0);
  const allPlatforms  = [...new Set(dayItems.flatMap(i => i.platforms.map(p => p.platform)))];

  const totalEl = document.createElement('span');
  totalEl.className = 'video-total-views';
  totalEl.textContent = `▶ ${fmt(totalViews)}`;
  meta.append(totalEl);

  const engDefs = [
    { key: 'likes',    icon: '♥',  val: totalLikes    },
    { key: 'comments', icon: '◉',  val: totalComments },
    { key: 'shares',   icon: '⬆',  val: totalShares   },
  ];
  for (const def of engDefs) {
    const supported = allPlatforms.some(pl => (PLATFORM_CAPS[pl] || {})[def.key]);
    if (!supported) continue;
    const span = document.createElement('span');
    span.className = 'eng-stat';
    span.textContent = `${def.icon} ${fmt(def.val)}`;
    meta.append(span);
  }

  header.append(dateLabel, meta);
  card.append(header);

  // One platform-versions block containing all items as item-groups
  const local = isLocalMode();
  const versions = document.createElement('div');
  versions.className = local ? 'platform-versions local' : 'platform-versions';

  for (const item of dayItems) {
    const group = document.createElement('div');
    group.className = 'item-group';

    for (const p of item.platforms) {
      const row = document.createElement('div');
      row.className = 'platform-version-row';
      row.dataset.href = p.url || '';

      row.addEventListener('click', (e) => {
        if (e.target.closest('.pv-select-btn')) return;
        if (e.target.closest('.pv-engagement')) return;
        if (p.url) window.open(p.url, '_blank', 'noopener,noreferrer');
      });

      const dot = document.createElement('span');
      dot.className = `platform-dot ${PLATFORM_COLORS[p.platform] || 'unknown'}`;

      const nameEl = document.createElement('span');
      nameEl.className = 'pv-name';
      nameEl.textContent = PLATFORM_LABELS[p.platform] || p.platform;

      const pvTitle = document.createElement('span');
      pvTitle.className = 'pv-title';
      if (isWithin7Days(p.published_at)) pvTitle.appendChild(newBadge());
      pvTitle.appendChild(document.createTextNode(p.title || ''));

      const pvViews = document.createElement('span');
      pvViews.className = 'pv-views';
      pvViews.textContent = `▶ ${fmt(p.views)}`;
      const prevPlatformViews = prevViewMap[`${p.platform}:${p.video_id}`] ?? null;
      if (!isWithin7Days(p.published_at)) {
        const pvGrowth = growthEl(pctChange(p.views || 0, prevPlatformViews));
        if (pvGrowth) pvViews.appendChild(pvGrowth);
      }

      const caps = PLATFORM_CAPS[p.platform] || {};
      const pvEng = document.createElement('span');
      pvEng.className = 'pv-engagement';
      if (caps.likes)    pvEng.appendChild(Object.assign(document.createElement('span'), { textContent: `♥ ${fmt(p.likes || 0)}` }));
      if (caps.comments) {
        const pvCommentBtn = document.createElement('button');
        pvCommentBtn.className = 'pv-comment-btn';
        pvCommentBtn.textContent = `◉ ${fmt(p.comments || 0)}`;
        pvCommentBtn.addEventListener('click', (e) => { e.stopPropagation(); showCommentsModal(item, p); });
        pvEng.appendChild(pvCommentBtn);
      }
      if (caps.shares) pvEng.appendChild(Object.assign(document.createElement('span'), { textContent: `⬆ ${fmt(p.shares || 0)}` }));

      row.append(dot, nameEl, pvTitle, pvViews, pvEng);

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

      group.appendChild(row);
    }

    if (local) {
      const btn = document.createElement('button');
      btn.className   = 'select-btn';
      btn.textContent = 'Select all';
      btn.addEventListener('click', (e) => {
        e.stopPropagation();
        toggleItemSelection(item.cardKey, item.videoIds, item.canonicalTitle, group, btn);
      });
      group.appendChild(btn);
    }

    versions.appendChild(group);
  }

  card.appendChild(versions);
  return card;
}

// ── Comments Modal ────────────────────────────────────────────────────────────

function showCommentsModal(item, singlePlatform = null) {
  const modal = document.getElementById('comments-modal');
  const titleEl = document.getElementById('modal-title');
  const body = document.getElementById('modal-body');
  if (!modal || !body) return;

  const platformLabel = singlePlatform ? (PLATFORM_LABELS[singlePlatform.platform] || singlePlatform.platform) : null;
  if (titleEl) titleEl.textContent = (item.canonicalTitle || 'Comments') + (platformLabel ? ` — ${platformLabel}` : '');
  body.innerHTML = '';

  // Show only the selected platform, or all platforms if none specified
  const platforms = singlePlatform ? [singlePlatform] : item.platforms;
  let hasAny = false;
  for (const p of platforms) {
    const texts = p.comment_texts || [];
    if (texts.length === 0) continue;
    hasAny = true;

    if (!singlePlatform) {
      const platformHeader = document.createElement('div');
      platformHeader.className = 'comment-platform-header';
      const dot = document.createElement('span');
      dot.className = `platform-dot ${PLATFORM_COLORS[p.platform] || 'unknown'}`;
      platformHeader.append(dot, PLATFORM_LABELS[p.platform] || p.platform);
      body.appendChild(platformHeader);
    }

    for (const text of texts) {
      const div = document.createElement('div');
      div.className = 'comment-item';
      div.textContent = text;
      body.appendChild(div);
    }
  }

  if (!hasAny) {
    const empty = document.createElement('p');
    empty.className = 'comment-empty';
    const unsupported = singlePlatform && !PLATFORM_CAPS[singlePlatform.platform]?.commentTexts;
    empty.textContent = unsupported
      ? 'Fetching comments for this platform is unsupported.'
      : 'No comments available.';
    body.appendChild(empty);
  }

  modal.hidden = false;
}

function initCommentsModal() {
  const modal = document.getElementById('comments-modal');
  if (!modal) return;
  document.getElementById('modal-close')?.addEventListener('click', () => { modal.hidden = true; });
  modal.addEventListener('click', (e) => { if (e.target === modal) modal.hidden = true; });
}

// ── Video List ────────────────────────────────────────────────────────────────

function renderVideoList(items, range) {
  renderedItems = items;
  const container = document.getElementById('video-groups');
  const heading   = document.querySelector('.section-heading');
  container.innerHTML = '';
  clearSelection();

  if (heading) {
    heading.textContent = items.length > 0 ? `Videos (${items.length})` : 'Videos';
  }

  if (!items.length) {
    const cutoff = rangeCutoff(range);
    const since  = cutoff ? ` since ${fmtDate(cutoff.toISOString())}` : '';
    container.innerHTML = `<p class="state-message">No videos${since}.</p>`;
    return;
  }

  // Sort newest week first, then by views within a week
  const sorted = [...items].sort((a, b) => {
    const da = a.publishedAt ? new Date(a.publishedAt) : new Date(0);
    const db = b.publishedAt ? new Date(b.publishedAt) : new Date(0);
    if (db - da !== 0) return db - da;
    return (b.totalViews || 0) - (a.totalViews || 0);
  });

  // Pre-compute total views per week and per day
  const weekTotals = {};
  const dayTotals  = {};
  for (const item of sorted) {
    const wk  = isoWeekKey(item.publishedAt);
    const day = item.publishedAt ? item.publishedAt.slice(0, 10) : 'unknown';
    weekTotals[wk]  = (weekTotals[wk]  || 0) + (item.totalViews || 0);
    dayTotals[day]  = (dayTotals[day]   || 0) + (item.totalViews || 0);
  }

  // Group items by day (preserving sort order)
  const dayGroups = [];
  const dayGroupMap = {};
  for (const item of sorted) {
    const dayKey = item.publishedAt ? item.publishedAt.slice(0, 10) : 'unknown';
    if (!dayGroupMap[dayKey]) {
      dayGroupMap[dayKey] = [];
      dayGroups.push({ dayKey, items: dayGroupMap[dayKey] });
    }
    dayGroupMap[dayKey].push(item);
  }

  let currentWeekKey = null;
  for (const { items: dayItems } of dayGroups) {
    const weekKey = isoWeekKey(dayItems[0].publishedAt);

    if (weekKey !== currentWeekKey) {
      currentWeekKey = weekKey;
      const header = document.createElement('div');
      header.className = 'week-heading';
      const labelSpan = document.createElement('span');
      labelSpan.textContent = isoWeekLabel(dayItems[0].publishedAt);
      const totalSpan = document.createElement('span');
      totalSpan.className = 'week-heading-total';
      totalSpan.textContent = `▶ ${fmt(weekTotals[weekKey])}`;
      header.append(labelSpan, totalSpan);
      container.appendChild(header);
    }

    container.appendChild(renderDayCard(dayItems));
  }
}

function isoWeekKey(publishedAt) {
  if (!publishedAt) return 'unknown';
  const d = new Date(publishedAt);
  const day = d.getUTCDay() || 7;
  const monday = new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate() - day + 1));
  return monday.toISOString().slice(0, 10);
}

function isoWeekLabel(publishedAt) {
  if (!publishedAt) return 'Unknown week';
  const d = new Date(publishedAt);
  const day = d.getUTCDay() || 7;
  const monday = new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate() - day + 1));
  const sunday = new Date(Date.UTC(monday.getUTCFullYear(), monday.getUTCMonth(), monday.getUTCDate() + 6));
  return `Week of ${fmtDateShort(monday.toISOString())} – ${fmtDateShort(sunday.toISOString())}`;
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
  renderRolling(allItems, activeMetric);
  renderWow(allItems, report.generated_at, activeMetric);
  const currItems = filterItems(allItems, range);

  // Previous equivalent period (date-shifted, same report) — for stat cards + chart
  let prevItems = null;
  const bounds = previousRangeBounds(range);
  if (bounds) prevItems = filterItemsByBounds(allItems, bounds.start, bounds.end);

  const { curr, prev } = renderSummary(currItems, prevItems, activeMetric);
  updateChart(curr, prev);
  renderVideoList(currItems, range);

  // If CI tab is currently visible, refresh it with the new analysis
  if (!document.getElementById('tab-ci')?.hidden) {
    renderContentIntelligence();
  }
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
  return getParam('range') || 'last30';
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
  document.querySelectorAll('.trend-toggle-btn[data-grouping]').forEach(btn => {
    btn.addEventListener('click', () => {
      const grouping = btn.dataset.grouping;
      if (grouping === trendGrouping) return;
      trendGrouping = grouping;
      document.querySelectorAll('.trend-toggle-btn[data-grouping]').forEach(b => {
        b.classList.toggle('active', b.dataset.grouping === grouping);
      });
      // Scale type change requires destroying and recreating the chart
      if (trendChartInstance) { trendChartInstance.destroy(); trendChartInstance = null; }
      if (currentReport) renderRolling(buildUnifiedList(currentReport));
    });
  });
}

// ── Metric Tabs ───────────────────────────────────────────────────────────────

function syncMetricTabs(metric) {
  document.querySelectorAll('.metric-tab').forEach(t => {
    t.classList.toggle('active', t.dataset.metric === metric);
  });
}

function initMetricTabs() {
  syncMetricTabs(activeMetric);
  document.querySelectorAll('.metric-tab').forEach(tab => {
    tab.addEventListener('click', () => {
      const m = tab.dataset.metric;
      if (m === activeMetric) return;
      activeMetric = m;
      syncMetricTabs(m);
      // Destroy trend chart so it re-initialises with correct data
      if (trendChartInstance) { trendChartInstance.destroy(); trendChartInstance = null; }
      if (currentReport) renderReport(currentReport, getActiveRange());
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

function initShareButton() {
  const btn = document.getElementById('share-btn');
  if (!btn) return;
  btn.addEventListener('click', async () => {
    try {
      await navigator.clipboard.writeText(window.location.href);
      const orig = btn.textContent;
      btn.textContent = 'Copied!';
      setTimeout(() => { btn.textContent = orig; }, 2000);
    } catch (_) {
      // Fallback for browsers that block clipboard without user gesture
      prompt('Copy this link:', window.location.href);
    }
  });
}

function initExportButton() {
  const btn = document.getElementById('export-btn');
  if (!btn) return;
  btn.addEventListener('click', async () => {
    if (!window.html2canvas) return;
    const onCI = !document.getElementById('tab-ci')?.hidden;
    if (!onCI) document.body.classList.add('exporting');
    try {
      const target = onCI ? document.getElementById('tab-ci') : document.body;
      const canvas = await html2canvas(target, {
        backgroundColor: '#0f1117',
        scale: 2,
        useCORS: true,
        logging: false,
      });
      const a = document.createElement('a');
      a.href = canvas.toDataURL('image/png');
      a.download = onCI ? 'devrel-content-intelligence.png' : 'devrel-dashboard.png';
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

    const [loaded, loadedPrev, loadedTranscripts, loadedAnalysis] = await Promise.all([
      loadData(`reports/${reportID}.json`),
      prevEntry ? loadData(`reports/${prevEntry.id}.json`).catch(() => null) : Promise.resolve(null),
      loadData('transcripts.json').catch(() => null),
      loadData(`analysis/${reportID}.json`).catch(() => null),
    ]);

    currentReport   = loaded;
    previousReport  = loadedPrev;
    transcriptStore = loadedTranscripts?.transcripts || {};
    currentAnalysis = loadedAnalysis || null;
    prevViewMap    = buildPrevViewMap(previousReport);

    renderReport(currentReport, getActiveRange());
  } catch (err) {
    document.getElementById('video-groups').innerHTML =
      `<p class="state-message">Failed to load report: ${err.message}</p>`;
  }
}

// ── View Decay Curve ──────────────────────────────────────────────────────────
// Fits a per-platform power-law exponent α from historical report data.
// For a video at age D with V views, projected views at age T = V * (T/D)^α.

async function computeDecayCurve(entries) {
  const msPerDay   = 86400000;
  const timeSeries = {}; // "platform:videoId" → [{ageDays, views}, ...]

  await Promise.all(entries.map(async entry => {
    try {
      const report = await loadData(entry.file);
      const reportDate = new Date(entry.generated_at);
      for (const group of report.video_groups || []) {
        for (const [plat, pd] of Object.entries(group.platforms || {})) {
          if (!pd.published_at || !pd.video_id || pd.views == null) continue;
          const ageDays = (reportDate - new Date(pd.published_at)) / msPerDay;
          if (ageDays < 0.5) continue;
          const key = plat + ':' + pd.video_id;
          (timeSeries[key] ??= []).push({ ageDays, views: pd.views });
        }
      }
    } catch (_) {}
  }));

  const buckets = { youtube: [], tiktok: [], linkedin: [] };

  for (const [key, pts] of Object.entries(timeSeries)) {
    const plat = key.split(':')[0];
    if (!buckets[plat]) continue;
    pts.sort((a, b) => a.ageDays - b.ageDays);
    for (let i = 0; i < pts.length - 1; i++) {
      const p1 = pts[i], p2 = pts[i + 1];
      if (p1.ageDays < 7)              continue; // skip viral-spike phase; fit post-spike tail only
      if (p2.ageDays - p1.ageDays < 3) continue; // too close — noisy ratio
      if (p1.views <= 0 || p2.views <= p1.views) continue;
      const α = Math.log(p2.views / p1.views) / Math.log(p2.ageDays / p1.ageDays);
      if (α >= 0.05 && α <= 0.7) buckets[plat].push(α); // >0.7 implies accelerating views — physically wrong
    }
  }

  const median = arr => {
    if (arr.length < 5) return 0.35; // conservative fallback
    const s = [...arr].sort((a, b) => a - b);
    return s[Math.floor(s.length / 2)];
  };

  return {
    youtube:  median(buckets.youtube),
    tiktok:   median(buckets.tiktok),
    linkedin: median(buckets.linkedin),
  };
}

async function init() {
  initRangeTabs();
  initMetricTabs();
  initTrendToggle();
  initMergeBar();
  initCommentsModal();
  initChart();
  initShareButton();
  initExportButton();
  initTabs();
  initContentIntelligence();

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

  allReportEntries = (index.reports || []).slice().sort((a, b) =>
    new Date(b.generated_at) - new Date(a.generated_at)
  );
  if (!allReportEntries.length) {
    document.getElementById('video-groups').innerHTML =
      '<p class="state-message">No reports yet. Run: <code>go run ./cmd/fetch --skip-linkedin</code></p>';
    return;
  }

  const newestID  = (index.reports || [])[0]?.id;
  const paramID   = getParam('report');
  const currentID = allReportEntries.find(e => e.id === paramID) ? paramID : allReportEntries[0].id;
  renderReportSelector(allReportEntries, currentID);

  // Stamp the report ID into the URL so a copied link always points to the right report
  const initUrl = new URL(window.location.href);
  initUrl.searchParams.set('report', currentID);
  window.history.replaceState({}, '', initUrl.toString());

  await loadAndRender(currentID);

  // Notify if the user arrived with a ?report= link pointing at a known-but-older report
  if (paramID && newestID && paramID !== newestID && allReportEntries.find(e => e.id === paramID)) {
    showUpdateNotice(
      'You\'re viewing an older report.',
      'View latest',
      () => {
        setParams({ report: newestID });
        const sel = document.getElementById('report-select');
        if (sel) sel.value = newestID;
        loadAndRender(newestID);
        document.querySelector('.update-notice')?.remove();
      }
    );
  }

  // Fit decay curve in the background; re-render WoW once ready
  computeDecayCurve(allReportEntries).then(curve => {
    decayCurve = curve;
    if (currentReport) renderWow(buildUnifiedList(currentReport), currentReport.generated_at);
  });

  startReportPolling();
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

// ── Tab Switching ─────────────────────────────────────────────────────────────

function initTabs() {
  const tabBar = document.getElementById('tab-bar');
  if (!tabBar) return;

  const activeTab = getParam('tab') || 'stats';
  setActiveTab(activeTab, false);

  tabBar.addEventListener('click', e => {
    const btn = e.target.closest('.tab-btn');
    if (!btn) return;
    const tab = btn.dataset.tab;
    setActiveTab(tab, true);
    if (tab === 'ci') renderContentIntelligence();
  });
}

function setActiveTab(tab, pushState) {
  document.querySelectorAll('.tab-btn').forEach(b => {
    b.classList.toggle('active', b.dataset.tab === tab);
  });
  const statsEl = document.getElementById('tab-stats');
  const ciEl    = document.getElementById('tab-ci');
  if (statsEl) statsEl.hidden = tab !== 'stats';
  if (ciEl)    ciEl.hidden    = tab !== 'ci';
  if (pushState) setParams({ tab: tab === 'stats' ? null : tab });
}

// ── Content Intelligence ──────────────────────────────────────────────────────

function initContentIntelligence() {
  // No setup needed — CI tab is purely display-driven from stored analysis files.
}

function renderContentIntelligence() {
  const metaEl    = document.getElementById('ci-meta');
  const resultsEl = document.getElementById('ci-results');
  if (!resultsEl) return;

  if (!currentAnalysis) {
    resultsEl.innerHTML = '<div class="ci-result-card"><h4>No analysis yet</h4><p>Add <code>ANTHROPIC_API_KEY</code> to your <code>.env</code> file and re-run <code>go run ./cmd/fetch</code> to generate analysis for this report.</p></div>';
    if (metaEl) metaEl.textContent = '';
    return;
  }

  if (metaEl) {
    const date = currentAnalysis.generated_at
      ? new Date(currentAnalysis.generated_at).toLocaleString(undefined, { month: 'short', day: 'numeric', year: 'numeric', hour: '2-digit', minute: '2-digit' })
      : '';
    metaEl.textContent = `${currentAnalysis.video_count} videos • ${currentAnalysis.model} • ${date}`;
  }

  resultsEl.innerHTML = renderAnalysisText(currentAnalysis.text || '');
}

function renderAnalysisText(text) {
  // Strip top-level h1 title if present (e.g. "# DevRel Short-Form Video Analysis")
  const stripped = text.replace(/^#\s+[^\n]*\n*/m, '').trim();

  // Split on h2 headings (##) — these are the main sections
  const sections = stripped.split(/^##\s+/m).filter(s => s.trim());
  if (sections.length === 0) {
    return `<div class="ci-result-card">${marked.parse(stripped)}</div>`;
  }
  return sections.map(section => {
    const newlineIdx = section.indexOf('\n');
    const heading = newlineIdx >= 0 ? section.slice(0, newlineIdx).trim() : section.trim();
    const body    = newlineIdx >= 0 ? section.slice(newlineIdx + 1).trim() : '';
    return `<div class="ci-result-card"><h4>${escapeHTML(heading)}</h4>${marked.parse(body)}</div>`;
  }).join('');
}

function escapeHTML(str) {
  return String(str)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}
