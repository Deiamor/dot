'use strict';

// ── Chart defaults ────────────────────────────────────────────────────────────
Chart.defaults.color = '#8892b0';
Chart.defaults.borderColor = '#2e3250';
Chart.defaults.font.family = "'Inter', system-ui, sans-serif";
Chart.defaults.font.size = 12;

const CHART_OPTS = {
  responsive: true,
  animation: false,
  interaction: { mode: 'index', intersect: false },
  plugins: { legend: { display: false }, tooltip: { backgroundColor: '#1a1d27', borderColor: '#2e3250', borderWidth: 1 } },
  scales: {
    x: { grid: { color: '#1e2235' }, ticks: { maxTicksLimit: 8, maxRotation: 0 } },
    y: { grid: { color: '#1e2235' } }
  }
};

function makeChart(id, label, color) {
  const ctx = document.getElementById(id).getContext('2d');
  return new Chart(ctx, {
    type: 'line',
    data: {
      labels: [],
      datasets: [{ label, data: [], borderColor: color, backgroundColor: color + '18',
        borderWidth: 2, pointRadius: 0, fill: true, tension: 0.2 }]
    },
    options: { ...CHART_OPTS }
  });
}

function makeBarChart(id, label, posColor, negColor) {
  const ctx = document.getElementById(id).getContext('2d');
  return new Chart(ctx, {
    type: 'bar',
    data: { labels: [], datasets: [{ label, data: [], backgroundColor: [], borderRadius: 3 }] },
    options: { ...CHART_OPTS }
  });
}

// ── Charts ────────────────────────────────────────────────────────────────────
const MAX_POINTS = 500;

const equityChart   = makeChart('chart-equity',   'Equity',   '#6366f1');
const priceChart    = makeChart('chart-price',    'Mid',      '#3b82f6');
const posChart      = makeChart('chart-position', 'Position', '#f59e0b');

function pushPoint(chart, time, value, max = MAX_POINTS) {
  const label = typeof time === 'string' ? time.slice(11, 19) : new Date(time).toLocaleTimeString();
  chart.data.labels.push(label);
  chart.data.datasets[0].data.push(value);
  if (chart.data.labels.length > max) {
    chart.data.labels.shift();
    chart.data.datasets[0].data.shift();
  }
  chart.update('none');
}

// ── Tabs ──────────────────────────────────────────────────────────────────────
document.querySelectorAll('nav button').forEach(btn => {
  btn.addEventListener('click', () => {
    document.querySelectorAll('nav button').forEach(b => b.classList.remove('active'));
    document.querySelectorAll('.tab-panel').forEach(p => p.classList.remove('active'));
    btn.classList.add('active');
    document.getElementById('tab-' + btn.dataset.tab).classList.add('active');
    if (btn.dataset.tab === 'backtest') loadDatasets();
    if (btn.dataset.tab === 'data') loadDatasets();
  });
});

// ── SSE connection ────────────────────────────────────────────────────────────
const connDot   = document.getElementById('conn-dot');
const stateBadge = document.getElementById('state-badge');

let fillsData = [];
const MAX_FILLS = 50;

function connectSSE() {
  const es = new EventSource('/api/events');

  es.onopen = () => { connDot.classList.add('connected'); };
  es.onerror = () => {
    connDot.classList.remove('connected');
    setTimeout(connectSSE, 3000);
  };

  es.onmessage = (e) => {
    let msg;
    try { msg = JSON.parse(e.data); } catch { return; }
    handleEvent(msg);
  };
}

function handleEvent(msg) {
  const { type, data } = msg;
  switch (type) {
    case 'tick':       onTick(data);       break;
    case 'fill':       onFill(data);       break;
    case 'equity_point': onEquityPoint(data); break;
    case 'state':      onState(data.state); break;
  }
}

function onState(state) {
  stateBadge.textContent = state || 'IDLE';
  stateBadge.className = '';
  if (state) stateBadge.classList.add(state);
}

function onTick(d) {
  setText('m-mid',     fmt(d.mid, 2));
  setText('m-pos',     fmtPos(d.position));
  setText('m-spread',  fmt(d.spreadBps, 2));
  setText('m-funding', fmtPct(d.fundingRate, 4));
  if (d.equity) {
    const init = equityChart.data.datasets[0].data[0] || d.equity;
    const upnl = d.equity - init;
    setText('m-equity', fmt(d.equity, 2));
    setValued('m-upnl', upnl, fmt(upnl, 2));
  }
  if (d.state) onState(d.state);
  pushPoint(priceChart, d.time, d.mid);
  pushPoint(posChart,   d.time, d.position);
}

function onEquityPoint(d) {
  pushPoint(equityChart, d.time, d.equity);
}

function onFill(d) {
  fillsData.unshift(d);
  if (fillsData.length > MAX_FILLS) fillsData.pop();
  renderFills();
}

function renderFills() {
  const tbody = document.getElementById('fills-body');
  if (!fillsData.length) {
    tbody.innerHTML = '<tr><td colspan="7" style="text-align:center;color:var(--text2);padding:24px">No fills yet</td></tr>';
    return;
  }
  tbody.innerHTML = fillsData.map(f => {
    const t = new Date(f.time).toLocaleTimeString();
    const pnlClass = f.realizedPnl > 0 ? 'pnl-pos' : f.realizedPnl < 0 ? 'pnl-neg' : '';
    const sideClass = f.side === 'BUY' ? 'badge-buy' : 'badge-sell';
    return `<tr>
      <td>${t}</td>
      <td>${f.symbol}</td>
      <td class="${sideClass}">${f.side}</td>
      <td>${fmt(f.price, 2)}</td>
      <td>${fmt(f.qty, 6)}</td>
      <td>${fmt(f.fee, 4)}</td>
      <td class="${pnlClass}">${fmt(f.realizedPnl, 4)}</td>
    </tr>`;
  }).join('');
}

// ── Dataset loading ───────────────────────────────────────────────────────────
let datasets = [];
let selectedDataset = null;

async function loadDatasets() {
  const res = await fetch('/api/datasets');
  datasets = await res.json() || [];

  // Update backtest select.
  const sel = document.getElementById('bt-dataset');
  sel.innerHTML = '<option value="">— select dataset —</option>' +
    datasets.map(d => `<option value="${d.path}">${d.symbol} ${d.interval} (${d.candles.toLocaleString()} candles)</option>`).join('');
  sel.onchange = () => {
    selectedDataset = sel.value;
    document.getElementById('btn-run-bt').disabled = !selectedDataset;
  };

  // Update dataset list in Data tab.
  const list = document.getElementById('dataset-list');
  if (!datasets.length) {
    list.innerHTML = '<div style="color:var(--text2);font-size:13px">No datasets downloaded yet.</div>';
    return;
  }
  list.innerHTML = datasets.map(d => `
    <div class="dataset-item" data-path="${d.path}">
      <div class="ds-name">${d.symbol}</div>
      <div class="ds-meta">${d.interval} · ${d.startTime.slice(0,10)} → ${d.endTime.slice(0,10)}</div>
      <div class="ds-candles">${d.candles.toLocaleString()} candles</div>
    </div>
  `).join('');
}

// ── Download ──────────────────────────────────────────────────────────────────
document.getElementById('btn-download').addEventListener('click', async () => {
  const symbol   = document.getElementById('dl-symbol').value.trim().toUpperCase();
  const interval = document.getElementById('dl-interval').value;
  const start    = document.getElementById('dl-start').value;
  const end      = document.getElementById('dl-end').value;

  if (!symbol || !start || !end) { alert('Fill in all fields.'); return; }
  if (start >= end) { alert('End date must be after start date.'); return; }

  const btn = document.getElementById('btn-download');
  btn.disabled = true;
  const progWrap = document.getElementById('dl-progress');
  const fill     = document.getElementById('dl-fill');
  const label    = document.getElementById('dl-label');
  progWrap.style.display = 'block';
  fill.style.width = '2%';
  label.textContent = 'Connecting to Binance…';

  const es = new EventSource('/api/download?' + new URLSearchParams({
    symbol, interval,
    startTime: new Date(start).toISOString(),
    endTime:   new Date(end + 'T23:59:59').toISOString(),
  }));

  // POST via fetch SSE workaround: submit as POST, stream response manually.
  es.close();
  const res = await fetch('/api/download', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      symbol, interval,
      startTime: new Date(start).toISOString(),
      endTime:   new Date(end + 'T23:59:59').toISOString(),
    }),
  });

  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buf = '';

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    buf += decoder.decode(value, { stream: true });
    const lines = buf.split('\n');
    buf = lines.pop();
    for (const line of lines) {
      if (!line.startsWith('data: ')) continue;
      let p;
      try { p = JSON.parse(line.slice(6)); } catch { continue; }
      if (p.error) {
        label.textContent = '✗ Error: ' + p.error;
        fill.style.background = 'var(--red)';
        break;
      }
      if (p.total > 0) {
        const pct = Math.round(p.done / p.total * 100);
        fill.style.width = pct + '%';
        label.textContent = `Downloading… ${p.done} / ${p.total} chunks`;
      }
      if (p.completed) {
        fill.style.width = '100%';
        label.textContent = '✓ Download complete: ' + p.filePath;
        await loadDatasets();
      }
    }
  }

  btn.disabled = false;
});

document.getElementById('btn-refresh-datasets').addEventListener('click', loadDatasets);

// ── Backtest ──────────────────────────────────────────────────────────────────
let btEquityChart = null;

document.getElementById('btn-run-bt').addEventListener('click', async () => {
  const datasetPath = document.getElementById('bt-dataset').value;
  if (!datasetPath) return;

  const cfg = {
    datasetPath,
    initialBalance: +document.getElementById('bt-balance').value,
    gamma:       +document.getElementById('bt-gamma').value,
    kappa:       +document.getElementById('bt-kappa').value,
    sigma:       +document.getElementById('bt-sigma').value,
    alpha:       +document.getElementById('bt-alpha').value,
    orderSize:   +document.getElementById('bt-ordersize').value,
    maxInventory:+document.getElementById('bt-maxinv').value,
  };

  const btn = document.getElementById('btn-run-bt');
  const status = document.getElementById('bt-status');
  btn.disabled = true;
  status.textContent = 'Running…';

  try {
    const res = await fetch('/api/backtest', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(cfg),
    });
    if (!res.ok) throw new Error(await res.text());
    const result = await res.json();
    renderBacktestResults(result);
    status.textContent = 'Done.';
  } catch (e) {
    status.textContent = '✗ ' + e.message;
  } finally {
    btn.disabled = false;
  }
});

function renderBacktestResults(r) {
  document.getElementById('bt-results').style.display = 'block';

  const pnlEl = document.getElementById('r-pnl');
  pnlEl.textContent = fmt(r.netPnl, 2) + ' USDT';
  pnlEl.style.color = r.netPnl >= 0 ? 'var(--green)' : 'var(--red)';

  const retEl = document.getElementById('r-return');
  retEl.textContent = fmt(r.returnPct, 2) + '%';
  retEl.style.color = r.returnPct >= 0 ? 'var(--green)' : 'var(--red)';

  document.getElementById('r-fills').textContent  = r.numFills.toLocaleString();
  document.getElementById('r-winrate').textContent = fmt(r.winRate * 100, 1) + '%';
  document.getElementById('r-fees').textContent   = fmt(r.totalFees, 2) + ' USDT';
  document.getElementById('r-equity').textContent = fmt(r.finalEquity, 2) + ' USDT';

  // Equity curve from backtest.
  if (btEquityChart) btEquityChart.destroy();
  const ctx = document.getElementById('chart-bt-equity').getContext('2d');
  btEquityChart = new Chart(ctx, {
    type: 'line',
    data: {
      labels: r.equityCurve.map((_, i) => i),
      datasets: [{
        label: 'Equity', data: r.equityCurve,
        borderColor: '#6366f1', backgroundColor: '#6366f118',
        borderWidth: 2, pointRadius: 0, fill: true, tension: 0.2,
      }]
    },
    options: { ...CHART_OPTS }
  });
}

// ── Utility ───────────────────────────────────────────────────────────────────
function setText(id, text) { document.getElementById(id).textContent = text; }

function setValued(id, val, text) {
  const el = document.getElementById(id);
  el.textContent = text;
  el.className = 'value ' + (val > 0 ? 'pos' : val < 0 ? 'neg' : '');
}

function fmt(v, decimals = 2) {
  if (v == null || isNaN(v)) return '—';
  return v.toLocaleString(undefined, { minimumFractionDigits: decimals, maximumFractionDigits: decimals });
}

function fmtPos(v) {
  if (v == null) return '—';
  return (v >= 0 ? '+' : '') + fmt(v, 6);
}

function fmtPct(v, decimals = 4) {
  if (v == null || isNaN(v)) return '—';
  return (v * 100).toFixed(decimals) + '%';
}

// ── Default date range (last 30 days) ─────────────────────────────────────────
(function setDefaultDates() {
  const now = new Date();
  const past = new Date(now);
  past.setDate(past.getDate() - 30);
  document.getElementById('dl-end').value   = now.toISOString().slice(0, 10);
  document.getElementById('dl-start').value = past.toISOString().slice(0, 10);
})();

// ── Boot ──────────────────────────────────────────────────────────────────────
connectSSE();

// Restore snapshot on load.
fetch('/api/snapshot').then(r => r.json()).then(snap => {
  if (snap.state) onState(snap.state);
  if (snap.equity) snap.equity.forEach(pt => onEquityPoint(pt));
  if (snap.fills)  snap.fills.forEach(f => onFill(f));
  if (snap.lastTick?.mid) onTick(snap.lastTick);
}).catch(() => {});
