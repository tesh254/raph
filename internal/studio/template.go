package studio

const indexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>raph Studio</title>
  <style>
    *, *::before, *::after { box-sizing: border-box; }
    :root {
      --bg: #e9ecef;
      --bg-strong: #f6f8fb;
      --panel: rgba(252, 253, 248, 0.8);
      --panel-strong: rgba(255, 255, 255, 0.96);
      --ink: #17211d;
      --muted: #77828f;
      --line: rgba(84, 96, 112, 0.12);
      --line-strong: rgba(84, 96, 112, 0.22);
      --code: #2f79b7;
      --doc: #5dbb98;
      --memory: #f3a8bb;
      --accent: #2f6df6;
      --glow: rgba(47, 109, 246, 0.18);
      --shadow: 0 18px 46px rgba(76, 89, 107, 0.12);
      --font: "IBM Plex Sans", "Avenir Next", "Segoe UI", sans-serif;
      --mono: "IBM Plex Mono", ui-monospace, SFMono-Regular, Menlo, monospace;
    }
    html, body { height: 100%; }
    body {
      margin: 0;
      overflow: hidden;
      color: var(--ink);
      font-family: var(--font);
      background: linear-gradient(180deg, #edf0f3, #e7ebef);
    }
    button, input {
      font: inherit;
    }
    button, input {
      border-radius: 8px;
      border: 1px solid var(--line);
      min-height: 32px;
    }
    button {
      background: #fff;
      color: var(--ink);
      padding: 0 13px;
      cursor: pointer;
      transition: background 120ms ease, border-color 120ms ease, transform 120ms ease;
    }
    button:hover {
      border-color: var(--line-strong);
      transform: translateY(-1px);
    }
    button.primary {
      background: var(--accent);
      border-color: var(--accent);
      color: #fff;
    }
    button.ghost {
      background: transparent;
    }
    button.danger {
      background: #fff4f2;
      color: #bf4637;
      border-color: rgba(191, 70, 55, 0.24);
    }
    input {
      width: 100%;
      background: #fff;
      color: var(--ink);
      padding: 0 13px;
      outline: none;
    }
    input:focus {
      border-color: rgba(45, 148, 102, 0.42);
      box-shadow: 0 0 0 4px rgba(45, 148, 102, 0.12);
    }
    pre {
      margin: 0;
      max-height: 320px;
      overflow: auto;
      white-space: pre-wrap;
      word-break: break-word;
      border: 1px solid var(--line);
      border-radius: 10px;
      background: #fbfcfe;
      padding: 12px;
      font: 12px/1.55 var(--mono);
    }
    #app {
      position: fixed;
      inset: 0;
      display: grid;
      grid-template-columns: 244px 1fr;
      grid-template-rows: 58px 1fr;
      background: rgba(255,255,255,0.95);
    }
    #topbar {
      grid-column: 1 / 3;
      display: flex;
      align-items: center;
      gap: 10px;
      padding: 10px 14px;
      border-bottom: 1px solid rgba(84, 96, 112, 0.08);
      background: rgba(255,255,255,0.96);
      z-index: 8;
    }
    .brand {
      display: flex;
      align-items: baseline;
      gap: 10px;
      min-width: 170px;
    }
    .brand strong {
      font-size: 14px;
      line-height: 1;
      letter-spacing: -0.02em;
    }
    .brand strong span { color: var(--doc); }
    .brand small {
      color: var(--muted);
      font-size: 10px;
    }
    .topnav {
      display: flex;
      align-items: center;
      gap: 30px;
      margin-left: 18px;
    }
    .topnav button {
      min-height: auto;
      padding: 10px 0;
      border: 0;
      border-radius: 0;
      background: transparent;
      color: #a0a9b3;
      font-size: 11px;
      box-shadow: none;
      transform: none;
    }
    .topnav button.active {
      color: var(--ink);
      border-bottom: 2px solid var(--ink);
    }
    .topnav button[data-view].active {
      color: var(--ink);
    }
    .searchbox { margin-left: auto; width: 260px; flex: 0 0 260px; }
    .actorbox { width: 126px; flex: 0 0 126px; }
    .toolbar {
      display: flex;
      align-items: center;
      gap: 6px;
    }
    #summary {
      color: var(--muted);
      font-size: 11px;
      white-space: nowrap;
    }
    #sidebar {
      border-right: 1px solid rgba(84, 96, 112, 0.08);
      background: #fff;
      padding: 12px;
      overflow: auto;
    }
    .section-title {
      margin: 0 0 8px;
      color: var(--muted);
      font-size: 10px;
      font-weight: 700;
      letter-spacing: 0.04em;
      text-transform: none;
    }
    .metric-grid {
      display: grid;
      grid-template-columns: 1fr 1fr;
      gap: 8px;
    }
    .metric {
      border: 1px solid var(--line);
      border-radius: 8px;
      padding: 10px;
      background: #fbfcfe;
      box-shadow: none;
    }
    .metric strong {
      display: block;
      font-size: 26px;
      line-height: 1;
      letter-spacing: -0.04em;
    }
    .metric span {
      color: var(--muted);
      font-size: 12px;
    }
    .view-card,
    .activity-card,
    .agent-card {
      margin-top: 14px;
      border: 1px solid var(--line);
      border-radius: 8px;
      padding: 10px;
      background: #fff;
      box-shadow: none;
    }
    .view-card p,
    .agent-card p {
      margin: 0;
      color: var(--muted);
      font-size: 12px;
      line-height: 1.45;
    }
    .view-card strong {
      display: block;
      margin-bottom: 6px;
      font-size: 15px;
    }
    .legend {
      display: grid;
      gap: 7px;
      margin-top: 10px;
    }
    .legend-row {
      display: flex;
      align-items: center;
      gap: 9px;
      color: var(--muted);
      font-size: 12px;
    }
    .swatch {
      width: 10px;
      height: 10px;
      border-radius: 999px;
      box-shadow: 0 0 0 4px rgba(255,255,255,0.64);
    }
    .swatch.code { background: var(--code); }
    .swatch.documentation { background: var(--doc); }
    .swatch.memory { background: var(--memory); }
    .activity-summary {
      display: flex;
      flex-wrap: wrap;
      gap: 6px;
      margin-top: 8px;
    }
    .activity-pill {
      border-radius: 999px;
      padding: 4px 9px;
      background: #f3f6fa;
      color: #596473;
      font-size: 11px;
    }
    .activity-list {
      display: grid;
      gap: 7px;
      margin-top: 10px;
      max-height: 220px;
      overflow: auto;
    }
    .activity-item {
      border: 1px solid var(--line);
      border-radius: 8px;
      background: #fbfcfe;
      padding: 9px 10px;
      color: var(--muted);
      font-size: 11px;
      line-height: 1.45;
    }
    .activity-item strong { color: var(--ink); }
    .root-list {
      display: grid;
      gap: 8px;
    }
    .root-button {
      width: 100%;
      min-height: 38px;
      display: grid;
      grid-template-columns: 10px 1fr auto;
      align-items: center;
      gap: 10px;
      padding: 8px 10px;
      text-align: left;
      border-radius: 8px;
      background: #fff;
    }
    .root-button.active {
      background: #f7fbff;
      border-color: rgba(47, 109, 246, 0.22);
      box-shadow: none;
    }
    .dot {
      width: 10px;
      height: 10px;
      border-radius: 999px;
      background: var(--memory);
    }
    .dot.code { background: var(--code); }
    .dot.documentation { background: var(--doc); }
    .dot.memory { background: var(--memory); }
    .root-name {
      min-width: 0;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
      font-size: 13px;
      font-weight: 650;
    }
    .count-pill {
      border-radius: 999px;
      padding: 3px 8px;
      background: rgba(234, 238, 230, 0.92);
      color: var(--muted);
      font-size: 11px;
    }
    .agent-card .row {
      display: flex;
      gap: 8px;
      margin-top: 10px;
    }
    .agent-card .row button { flex: 1; }
    .agent-card pre { margin-top: 10px; }
    #stage {
      position: relative;
      overflow: hidden;
      cursor: grab;
      touch-action: none;
    }
    .surface {
      position: relative;
      overflow: auto;
      background: rgba(255,255,255,0.72);
    }
    .surface.hidden {
      display: none;
    }
    #stage.panning,
    #stage.dragging {
      cursor: grabbing;
    }
    #stage::before {
      content: '';
      position: absolute;
      inset: 0;
      background:
        radial-gradient(circle at center, rgba(255,255,255,0.65), transparent 54%),
        linear-gradient(rgba(84, 109, 96, 0.03) 1px, transparent 1px),
        linear-gradient(90deg, rgba(84, 109, 96, 0.03) 1px, transparent 1px);
      background-size: auto, 56px 56px, 56px 56px;
      background-position: center, center, center;
      pointer-events: none;
      opacity: 0.56;
    }
    #graph {
      position: absolute;
      inset: 0;
      width: 100%;
      height: 100%;
      display: block;
    }
    #overview-view,
    #data-view {
      padding: 18px;
    }
    .overview-grid {
      display: grid;
      grid-template-columns: minmax(320px, 1.2fr) minmax(280px, 0.8fr);
      gap: 16px;
    }
    .chart-card,
    .data-card {
      border: 1px solid var(--line);
      border-radius: 12px;
      background: rgba(255,255,255,0.94);
      padding: 14px;
      box-shadow: 0 8px 20px rgba(76, 89, 107, 0.06);
    }
    .chart-card h3,
    .data-card h3 {
      margin: 0 0 6px;
      font-size: 15px;
    }
    .chart-card p,
    .data-card p {
      margin: 0 0 10px;
      font-size: 12px;
      color: var(--muted);
    }
    .chart-svg {
      width: 100%;
      height: 260px;
      display: block;
    }
    .mini-bars {
      display: grid;
      gap: 9px;
    }
    .mini-bar-row {
      display: grid;
      grid-template-columns: 80px 1fr 44px;
      gap: 10px;
      align-items: center;
      font-size: 12px;
    }
    .mini-bar-track {
      height: 8px;
      border-radius: 999px;
      background: #eef3fb;
      overflow: hidden;
    }
    .mini-bar-fill {
      height: 100%;
      border-radius: 999px;
      background: linear-gradient(90deg, #8bc4ff, #2f6df6);
    }
    .sqlite-wrap {
      display: grid;
      gap: 14px;
    }
    .sqlite-table {
      border: 1px solid var(--line);
      border-radius: 10px;
      overflow: hidden;
      background: #fff;
    }
    .sqlite-head {
      padding: 10px 12px;
      border-bottom: 1px solid var(--line);
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 12px;
      background: #fbfcfe;
    }
    .sqlite-head strong {
      font-size: 13px;
    }
    .sqlite-table-scroll {
      overflow: auto;
      max-height: 300px;
    }
    .sqlite-table table {
      width: 100%;
      border-collapse: collapse;
      font: 12px/1.45 var(--mono);
    }
    .sqlite-table th,
    .sqlite-table td {
      padding: 8px 10px;
      border-bottom: 1px solid rgba(84, 96, 112, 0.08);
      text-align: left;
      vertical-align: top;
      white-space: nowrap;
    }
    .sqlite-table th {
      position: sticky;
      top: 0;
      background: #fbfcfe;
      z-index: 1;
    }
    .sqlite-empty {
      padding: 16px;
      color: var(--muted);
      font-size: 12px;
    }
    #focus-chip {
      position: absolute;
      min-width: 220px;
      max-width: 300px;
      padding: 10px 12px;
      border: 1px solid rgba(30, 48, 40, 0.12);
      border-radius: 12px;
      background: rgba(255,255,255,0.96);
      box-shadow: var(--shadow);
      backdrop-filter: blur(16px);
      pointer-events: none;
      opacity: 0;
      transform: translateY(8px);
      transition: opacity 140ms ease, transform 140ms ease;
    }
    #focus-chip.visible {
      opacity: 1;
      transform: translateY(0);
    }
    #focus-chip strong {
      display: block;
      font-size: 14px;
      line-height: 1.15;
      margin-bottom: 4px;
    }
    #focus-chip small,
    #focus-chip span {
      display: block;
      color: var(--muted);
      font-size: 12px;
      line-height: 1.35;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    .empty-state {
      position: absolute;
      left: 50%;
      top: 50%;
      transform: translate(-50%, -50%);
      max-width: 460px;
      padding: 20px;
      border: 1px solid var(--line);
      border-radius: 18px;
      background: var(--panel-strong);
      text-align: center;
      box-shadow: var(--shadow);
      color: var(--muted);
    }
    .empty-state strong {
      display: block;
      color: var(--ink);
      margin-bottom: 6px;
      font-size: 18px;
    }
    #properties {
      position: fixed;
      top: 92px;
      right: 24px;
      width: min(440px, calc(100vw - 36px));
      max-height: calc(100vh - 116px);
      overflow: hidden;
      display: none;
      flex-direction: column;
      border: 1px solid rgba(30, 48, 40, 0.12);
      border-radius: 12px;
      background: var(--panel-strong);
      box-shadow: var(--shadow);
      backdrop-filter: blur(18px);
      z-index: 12;
    }
    #properties.visible { display: flex; }
    #properties.collapsed .panel-body {
      display: none;
    }
    #properties.expanded {
      top: 72px;
      right: 18px;
      left: auto;
      width: min(720px, calc(100vw - 36px));
      max-height: calc(100vh - 92px);
    }
    .panel-head {
      display: flex;
      align-items: center;
      gap: 10px;
      padding: 12px;
      border-bottom: 1px solid var(--line);
      cursor: grab;
    }
    .panel-actions {
      display: flex;
      align-items: center;
      gap: 6px;
    }
    .panel-title {
      min-width: 0;
      flex: 1;
      font-weight: 700;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    .panel-body {
      padding: 14px;
      overflow: auto;
    }
    .property-grid {
      display: grid;
      grid-template-columns: 1fr 1fr;
      gap: 9px;
      margin-bottom: 12px;
    }
    .property {
      min-width: 0;
      border: 1px solid var(--line);
      border-radius: 10px;
      background: #fff;
      padding: 9px 10px;
    }
    .property label {
      display: block;
      margin-bottom: 4px;
      color: var(--muted);
      font-size: 10px;
      font-weight: 700;
      letter-spacing: 0.12em;
      text-transform: uppercase;
    }
    .property div {
      overflow-wrap: anywhere;
      font-size: 13px;
    }
    .wide { grid-column: 1 / -1; }
    .tabs {
      display: flex;
      gap: 7px;
      margin: 12px 0 10px;
    }
    .tabs button {
      flex: 1;
      min-height: 34px;
      font-size: 13px;
      background: #f5f7fb;
    }
    .tabs button.active {
      background: var(--accent);
      border-color: var(--accent);
      color: #fff;
    }
    .relation-list {
      display: grid;
      gap: 8px;
      margin-bottom: 12px;
    }
    .relation {
      display: grid;
      grid-template-columns: 74px 1fr;
      gap: 8px;
      align-items: start;
      border: 1px solid var(--line);
      border-radius: 10px;
      padding: 8px 9px;
      background: #fff;
      cursor: pointer;
    }
    .relation span:first-child {
      color: var(--muted);
      font-size: 11px;
      font-weight: 700;
      overflow-wrap: anywhere;
    }
    .relation strong {
      display: block;
      font-size: 13px;
      overflow-wrap: anywhere;
    }
    .relation small {
      color: var(--muted);
      font-size: 11px;
    }
    .actions {
      display: flex;
      gap: 8px;
      margin-top: 12px;
    }
    .actions button { flex: 1; }
    .toast {
      position: fixed;
      left: 50%;
      bottom: 18px;
      transform: translateX(-50%);
      border: 1px solid var(--line);
      border-radius: 999px;
      background: rgba(255,255,252,0.94);
      box-shadow: var(--shadow);
      padding: 9px 15px;
      color: var(--muted);
      font-size: 13px;
      display: none;
      z-index: 14;
    }
    .toast.visible { display: block; }
    @media (max-width: 980px) {
      #app {
        grid-template-columns: 1fr;
        grid-template-rows: auto 240px 1fr;
        inset: 0;
      }
      #topbar {
        grid-column: 1;
        flex-wrap: wrap;
      }
      .brand { min-width: 0; width: auto; }
      .topnav { order: 3; width: 100%; margin-left: 0; }
      .searchbox { order: 4; flex-basis: 100%; max-width: none; width: auto; }
      #summary { width: 100%; }
      #sidebar {
        grid-row: 2;
        border-right: 0;
        border-bottom: 1px solid rgba(41, 53, 46, 0.08);
      }
      .agent-card { display: none; }
      #stage { grid-row: 3; }
      #properties,
      #properties.expanded {
        top: auto;
        left: 12px;
        right: 12px;
        bottom: 12px;
        width: auto;
        max-height: 64vh;
      }
    }
  </style>
  <script src="https://cdn.jsdelivr.net/npm/d3@7/dist/d3.min.js"></script>
</head>
<body>
  <div id="app">
    <div id="topbar">
      <div class="brand">
        <strong>raph<span>Studio</span></strong>
        <small>local graph base</small>
      </div>
      <div class="topnav">
        <button data-view="overview">Overview</button>
        <button data-view="data">Data</button>
        <button data-view="graph" class="active">Graph</button>
      </div>
      <div class="searchbox"><input id="filter" type="search" placeholder="Filter knowledge, path, URL, content"></div>
      <div class="actorbox"><input id="actor" type="search" placeholder="Agent"></div>
      <div class="toolbar">
        <button id="fit">Fit</button>
        <button id="zoom-out">-</button>
        <button id="zoom-in">+</button>
        <button id="init-demo">Init Raph + example.com</button>
        <button id="clear-db" class="danger">Clear DB</button>
        <button id="reload" class="primary">Reload</button>
      </div>
      <small id="summary">Loading</small>
    </div>

    <aside id="sidebar">
      <div>
        <div class="section-title">Graph</div>
        <div class="metric-grid">
          <div class="metric"><strong id="node-count">0</strong><span>nodes</span></div>
          <div class="metric"><strong id="edge-count">0</strong><span>edges</span></div>
          <div class="metric"><strong id="visible-count">0</strong><span>visible</span></div>
          <div class="metric"><strong id="embed-count">0</strong><span>embedded</span></div>
        </div>
      </div>

      <div class="view-card">
        <div class="section-title">Select all nodes</div>
        <strong>Full graph scope</strong>
        <p>Filter graph, keep all nodes visible, inspect fast from canvas.</p>
        <div class="legend">
          <div class="legend-row"><span class="swatch code"></span><span>Code nodes</span></div>
          <div class="legend-row"><span class="swatch documentation"></span><span>Documentation nodes</span></div>
          <div class="legend-row"><span class="swatch memory"></span><span>Memory nodes</span></div>
        </div>
      </div>

      <div class="activity-card">
        <div class="section-title">Select connected nodes</div>
        <div id="activity-summary" class="activity-summary"></div>
        <div id="activity-list" class="activity-list"></div>
      </div>

      <div>
        <div class="section-title">Anchors</div>
        <div id="root-list" class="root-list"></div>
      </div>

      <div class="agent-card">
        <div class="section-title">Agent View</div>
        <p>Query Studio endpoints from same surface.</p>
        <input id="agent-query" type="search" placeholder="Query MCP search">
        <div class="row">
          <button id="agent-search" class="primary">Search</button>
          <button id="agent-neighbors">Neighbors</button>
        </div>
        <pre id="agent-output">No query yet.</pre>
      </div>
    </aside>

    <main id="overview-view" class="surface hidden">
      <div class="overview-grid">
        <section class="chart-card">
          <h3>Attribution Over Time</h3>
          <p>Actor usage over recent graph actions.</p>
          <svg id="activity-chart" class="chart-svg"></svg>
        </section>
        <section class="chart-card">
          <h3>Actor Share</h3>
          <p>Recent graph actions by actor.</p>
          <div id="actor-bars" class="mini-bars"></div>
        </section>
      </div>
    </main>

    <main id="data-view" class="surface hidden">
      <section class="data-card">
        <h3>SQLite Tables</h3>
        <p>Live store dump from embedded SQLite-compatible database.</p>
        <div id="sqlite-wrap" class="sqlite-wrap"></div>
      </section>
    </main>

    <main id="stage" class="surface">
      <canvas id="graph"></canvas>
      <div id="focus-chip" aria-hidden="true"></div>
      <div id="empty" class="empty-state" hidden>
        <strong>No graph data yet</strong>
        <span>Use Init Raph + example.com, or run raph init / raph crawl and then reload Studio.</span>
      </div>
    </main>
  </div>

  <section id="properties" aria-live="polite">
    <div id="properties-head" class="panel-head">
      <span class="dot" id="panel-dot"></span>
      <div id="panel-title" class="panel-title">Node</div>
      <div class="panel-actions">
        <button id="panel-collapse" class="ghost">Collapse</button>
        <button id="panel-expand" class="ghost">Expand</button>
        <button id="panel-close" class="ghost">Close</button>
      </div>
    </div>
    <div id="properties-body" class="panel-body"></div>
  </section>
  <div id="toast" class="toast"></div>

  <script>
    var state = {
      nodes: [],
      edges: [],
      byId: {},
      children: {},
      parents: {},
      roots: [],
      visible: {},
      expanded: {},
      selected: '',
      activeRoot: '',
      hovered: '',
      filter: '',
      details: {},
      positions: {},
      manualPositions: {},
      viewport: { scale: 1, panX: 0, panY: 0 },
      dragNode: '',
      draggingNode: false,
      dragOffsetX: 0,
      dragOffsetY: 0,
      isPanning: false,
      panStartX: 0,
      panStartY: 0,
      panOriginX: 0,
      panOriginY: 0,
      panMoved: false,
      actor: '',
      activity: [],
      sqliteTables: [],
      tab: 'content',
      view: 'graph',
      panelCollapsed: false,
      panelExpanded: false,
      drawFrame: 0,
      resizedFrame: 0,
      stageWidth: 0,
      stageHeight: 0
    };

    var els = {
      stage: document.getElementById('stage'),
      overviewView: document.getElementById('overview-view'),
      dataView: document.getElementById('data-view'),
      canvas: document.getElementById('graph'),
      chip: document.getElementById('focus-chip'),
      empty: document.getElementById('empty'),
      filter: document.getElementById('filter'),
      actor: document.getElementById('actor'),
      reload: document.getElementById('reload'),
      fit: document.getElementById('fit'),
      zoomOut: document.getElementById('zoom-out'),
      zoomIn: document.getElementById('zoom-in'),
      initDemo: document.getElementById('init-demo'),
      clearDB: document.getElementById('clear-db'),
      summary: document.getElementById('summary'),
      nodeCount: document.getElementById('node-count'),
      edgeCount: document.getElementById('edge-count'),
      visibleCount: document.getElementById('visible-count'),
      embedCount: document.getElementById('embed-count'),
      rootList: document.getElementById('root-list'),
      properties: document.getElementById('properties'),
      propertiesHead: document.getElementById('properties-head'),
      propertiesBody: document.getElementById('properties-body'),
      panelTitle: document.getElementById('panel-title'),
      panelDot: document.getElementById('panel-dot'),
      panelCollapse: document.getElementById('panel-collapse'),
      panelExpand: document.getElementById('panel-expand'),
      panelClose: document.getElementById('panel-close'),
      agentQuery: document.getElementById('agent-query'),
      agentSearch: document.getElementById('agent-search'),
      agentNeighbors: document.getElementById('agent-neighbors'),
      agentOutput: document.getElementById('agent-output'),
      activitySummary: document.getElementById('activity-summary'),
      activityList: document.getElementById('activity-list'),
      activityChart: document.getElementById('activity-chart'),
      actorBars: document.getElementById('actor-bars'),
      sqliteWrap: document.getElementById('sqlite-wrap'),
      navButtons: Array.prototype.slice.call(document.querySelectorAll('.topnav button[data-view]')),
      toast: document.getElementById('toast')
    };

    var ctx = els.canvas.getContext('2d');

    function escapeHTML(value) {
      return String(value == null ? '' : value).replace(/[&<>"']/g, function(ch) {
        return ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'})[ch];
      });
    }

    function storageKey() {
      return 'raph-studio-activity';
    }

    function shortID(id) {
      id = String(id || '');
      return id.length > 22 ? id.slice(0, 10) + '...' + id.slice(-8) : id;
    }

    function nodeName(node) {
      if (!node) return '';
      return node.name || node.url || node.path || node.id;
    }

    function nodeKind(node) {
      if (!node) return 'memory';
      return node.domain === 'code' ? 'code' : node.domain === 'documentation' ? 'documentation' : 'memory';
    }

    function nodeColor(node) {
      if (!node) return '#7f5bb4';
      return node.domain === 'code' ? '#2f79b7' : node.domain === 'documentation' ? '#2d9466' : '#7f5bb4';
    }

    function childCount(id) {
      return (state.children[id] || []).length;
    }

    function parentCount(id) {
      return (state.parents[id] || []).length;
    }

    function degreeOf(id) {
      return childCount(id) + parentCount(id);
    }

    function neighborsOf(id) {
      var set = {};
      (state.children[id] || []).forEach(function(nextID) { set[nextID] = true; });
      (state.parents[id] || []).forEach(function(nextID) { set[nextID] = true; });
      return Object.keys(set);
    }

    function edgeTypeBetween(source, target) {
      for (var i = 0; i < state.edges.length; i++) {
        var edge = state.edges[i];
        if (edge.source_id === source && edge.target_id === target) return edge.type;
      }
      return '';
    }

    function loadActivity() {
      try {
        var raw = window.localStorage.getItem(storageKey());
        return raw ? JSON.parse(raw) : [];
      } catch (error) {
        return [];
      }
    }

    function saveActivity() {
      try {
        window.localStorage.setItem(storageKey(), JSON.stringify(state.activity.slice(0, 40)));
      } catch (error) {}
    }

    function actorName() {
      var value = String(els.actor.value || '').trim();
      return value || 'browser';
    }

    function focusID() {
      return state.hovered || state.selected;
    }

    function isRelatedToFocus(id) {
      var focus = focusID();
      if (!focus) return true;
      if (id === focus) return true;
      return (state.children[focus] || []).indexOf(id) !== -1 ||
        (state.parents[focus] || []).indexOf(id) !== -1 ||
        (state.children[id] || []).indexOf(focus) !== -1 ||
        (state.parents[id] || []).indexOf(focus) !== -1;
    }

    function recordActivity(action, id, note) {
      var entry = {
        action: action,
        id: id || '',
        note: note || '',
        actor: actorName(),
        node: id && state.byId[id] ? nodeName(state.byId[id]) : '',
        at: new Date().toISOString()
      };
      state.activity.unshift(entry);
      state.activity = state.activity.slice(0, 40);
      saveActivity();
      renderActivity();
    }

    function renderActivity() {
      var counts = {};
      state.activity.forEach(function(entry) {
        counts[entry.actor] = (counts[entry.actor] || 0) + 1;
      });
      var summary = Object.keys(counts).sort(function(a, b) {
        return counts[b] - counts[a];
      }).map(function(actor) {
        return '<span class="activity-pill">' + escapeHTML(actor) + ' · ' + counts[actor] + '</span>';
      }).join('');
      if (!summary) summary = '<span class="activity-pill">No activity yet</span>';
      els.activitySummary.innerHTML = summary;

      els.activityList.innerHTML = state.activity.slice(0, 14).map(function(entry) {
        return '<div class="activity-item">' +
          '<strong>' + escapeHTML(entry.actor) + '</strong> ' +
          escapeHTML(entry.action) + (entry.node ? ' · ' + escapeHTML(entry.node) : '') +
          (entry.note ? '<br>' + escapeHTML(entry.note) : '') +
        '</div>';
      }).join('') || '<div class="activity-item">No graph activity yet.</div>';
      renderOverview();
    }

    function renderOverview() {
      renderActorBars();
      renderActivityChart();
    }

    function renderActorBars() {
      var counts = {};
      state.activity.forEach(function(entry) {
        counts[entry.actor] = (counts[entry.actor] || 0) + 1;
      });
      var actors = Object.keys(counts).sort(function(a, b) { return counts[b] - counts[a]; });
      if (!actors.length) {
        els.actorBars.innerHTML = '<div class="sqlite-empty">No attribution activity yet.</div>';
        return;
      }
      var max = counts[actors[0]] || 1;
      els.actorBars.innerHTML = actors.map(function(actor) {
        var width = Math.max(6, Math.round((counts[actor] / max) * 100));
        return '<div class="mini-bar-row">' +
          '<span>' + escapeHTML(actor) + '</span>' +
          '<div class="mini-bar-track"><div class="mini-bar-fill" style="width:' + width + '%"></div></div>' +
          '<strong>' + counts[actor] + '</strong>' +
        '</div>';
      }).join('');
    }

    function renderActivityChart() {
      if (!window.d3 || !els.activityChart) return;
      var svg = window.d3.select(els.activityChart);
      svg.selectAll('*').remove();
      var width = els.activityChart.clientWidth || 640;
      var height = els.activityChart.clientHeight || 260;
      svg.attr('viewBox', '0 0 ' + width + ' ' + height);

      if (!state.activity.length) {
        svg.append('text')
          .attr('x', 16)
          .attr('y', 24)
          .attr('fill', '#77828f')
          .style('font-size', '12px')
          .text('No activity yet.');
        return;
      }

      var bucketCount = 12;
      var end = new Date();
      var start = new Date(end.getTime() - bucketCount * 60 * 60 * 1000);
      var actors = Array.from(new Set(state.activity.map(function(entry) { return entry.actor || 'browser'; })));
      var buckets = [];
      for (var i = 0; i < bucketCount; i++) {
        buckets.push({
          time: new Date(start.getTime() + i * 60 * 60 * 1000)
        });
        actors.forEach(function(actor) { buckets[i][actor] = 0; });
      }
      state.activity.forEach(function(entry) {
        var at = new Date(entry.at);
        if (at < start || at > end) return;
        var idx = Math.min(bucketCount - 1, Math.max(0, Math.floor((at.getTime() - start.getTime()) / (60 * 60 * 1000))));
        buckets[idx][entry.actor || 'browser'] += 1;
      });

      var stack = window.d3.stack().keys(actors)(buckets);
      var margin = { top: 14, right: 12, bottom: 28, left: 36 };
      var innerWidth = width - margin.left - margin.right;
      var innerHeight = height - margin.top - margin.bottom;
      var root = svg.append('g').attr('transform', 'translate(' + margin.left + ',' + margin.top + ')');
      var x = window.d3.scalePoint().domain(buckets.map(function(bucket) { return bucket.time; })).range([0, innerWidth]);
      var y = window.d3.scaleLinear()
        .domain([0, window.d3.max(buckets, function(bucket) {
          return actors.reduce(function(sum, actor) { return sum + bucket[actor]; }, 0);
        }) || 1])
        .nice()
        .range([innerHeight, 0]);
      var colors = window.d3.scaleOrdinal()
        .domain(actors)
        .range(['#2f6df6', '#61b9a0', '#f3a8bb', '#9a7cff', '#ffb75c']);

      root.append('g')
        .attr('transform', 'translate(0,' + innerHeight + ')')
        .call(window.d3.axisBottom(x).tickFormat(window.d3.timeFormat('%H:%M')).tickSizeOuter(0).ticks(6))
        .call(function(g) { g.selectAll('text').attr('fill', '#77828f').style('font-size', '10px'); })
        .call(function(g) { g.selectAll('line,path').attr('stroke', 'rgba(84,96,112,0.15)'); });

      root.append('g')
        .call(window.d3.axisLeft(y).ticks(4).tickSize(-innerWidth))
        .call(function(g) { g.selectAll('text').attr('fill', '#77828f').style('font-size', '10px'); })
        .call(function(g) { g.selectAll('line').attr('stroke', 'rgba(84,96,112,0.12)'); })
        .call(function(g) { g.select('path').attr('stroke', 'rgba(84,96,112,0.15)'); });

      var area = window.d3.area()
        .x(function(d) { return x(d.data.time); })
        .y0(function(d) { return y(d[0]); })
        .y1(function(d) { return y(d[1]); })
        .curve(window.d3.curveCatmullRom.alpha(0.5));

      root.selectAll('.layer')
        .data(stack)
        .enter()
        .append('path')
        .attr('fill', function(d) { return colors(d.key); })
        .attr('fill-opacity', 0.32)
        .attr('stroke', function(d) { return colors(d.key); })
        .attr('stroke-width', 1.4)
        .attr('d', area);
    }

    function renderSQLite() {
      if (!state.sqliteTables.length) {
        els.sqliteWrap.innerHTML = '<div class="sqlite-empty">No SQLite data loaded yet.</div>';
        return;
      }
      els.sqliteWrap.innerHTML = state.sqliteTables.map(function(table) {
        var head = '<div class="sqlite-head"><strong>' + escapeHTML(table.name) + '</strong><span>' + table.rows.length + ' rows</span></div>';
        if (!table.rows.length) {
          return '<section class="sqlite-table">' + head + '<div class="sqlite-empty">No rows.</div></section>';
        }
        var columns = table.columns || Object.keys(table.rows[0] || {});
        var header = '<tr>' + columns.map(function(column) { return '<th>' + escapeHTML(column) + '</th>'; }).join('') + '</tr>';
        var rows = table.rows.map(function(row) {
          return '<tr>' + columns.map(function(column) {
            return '<td>' + escapeHTML(row[column] == null ? '' : String(row[column])) + '</td>';
          }).join('') + '</tr>';
        }).join('');
        return '<section class="sqlite-table">' +
          head +
          '<div class="sqlite-table-scroll"><table><thead>' + header + '</thead><tbody>' + rows + '</tbody></table></div>' +
        '</section>';
      }).join('');
    }

    function setView(view) {
      state.view = view;
      els.navButtons.forEach(function(button) {
        button.classList.toggle('active', button.dataset.view === view);
      });
      els.stage.classList.toggle('hidden', view !== 'graph');
      els.overviewView.classList.toggle('hidden', view !== 'overview');
      els.dataView.classList.toggle('hidden', view !== 'data');
      if (view === 'overview') renderOverview();
      if (view === 'data') loadSQLiteData();
      if (view === 'graph') scheduleResizeDraw();
    }

    function applyPanelState() {
      els.properties.classList.toggle('collapsed', state.panelCollapsed);
      els.properties.classList.toggle('expanded', state.panelExpanded);
      els.panelCollapse.textContent = state.panelCollapsed ? 'Expand Body' : 'Collapse';
      els.panelExpand.textContent = state.panelExpanded ? 'Window' : 'Expand';
    }

    function resetVisibleToFocus(id) {
      state.visible = {};
      state.expanded = {};
      state.visible[id] = true;
      (state.parents[id] || []).forEach(function(parentID) { state.visible[parentID] = true; });
      (state.children[id] || []).forEach(function(childID) { state.visible[childID] = true; });
      state.expanded[id] = true;
    }

    function buildComponent(start, allowed) {
      var component = [];
      var queue = [start];
      var seen = {};
      while (queue.length) {
        var id = queue.shift();
        if (seen[id] || !allowed[id]) continue;
        seen[id] = true;
        component.push(id);
        neighborsOf(id).forEach(function(nextID) {
          if (!seen[nextID] && allowed[nextID]) queue.push(nextID);
        });
      }
      return component;
    }

    function connectedComponents(list) {
      var allowed = {};
      list.forEach(function(node) { allowed[node.id] = true; });
      var remaining = list.map(function(node) { return node.id; });
      var components = [];
      while (remaining.length) {
        var start = remaining.shift();
        if (!allowed[start]) continue;
        var component = buildComponent(start, allowed);
        components.push(component);
        component.forEach(function(id) { delete allowed[id]; });
        remaining = remaining.filter(function(id) { return allowed[id]; });
      }
      return components;
    }

    function distanceMap(start, allowed) {
      var distances = {};
      var queue = [{ id: start, distance: 0 }];
      while (queue.length) {
        var item = queue.shift();
        if (distances[item.id] != null) continue;
        if (allowed && !allowed[item.id]) continue;
        distances[item.id] = item.distance;
        neighborsOf(item.id).forEach(function(nextID) {
          if (distances[nextID] == null && (!allowed || allowed[nextID])) {
            queue.push({ id: nextID, distance: item.distance + 1 });
          }
        });
      }
      return distances;
    }

    function visibleNodes() {
      var list = state.nodes.filter(function(node) {
        if (!state.visible[node.id]) return false;
        if (!state.filter) return true;
        var haystack = [node.id, node.name, node.type, node.domain, node.url, node.path, node.content].join(' ').toLowerCase();
        return haystack.indexOf(state.filter) !== -1;
      });
      list.sort(function(a, b) {
        var da = degreeOf(a.id);
        var db = degreeOf(b.id);
        if (da !== db) return db - da;
        return nodeName(a).localeCompare(nodeName(b));
      });
      return list;
    }

    function graphPosition(id) {
      return state.manualPositions[id] || state.positions[id];
    }

    function ensureCanvasSize() {
      var rect = els.stage.getBoundingClientRect();
      var width = Math.max(1, Math.floor(rect.width));
      var height = Math.max(1, Math.floor(rect.height));
      var dpr = window.devicePixelRatio || 1;
      if (state.stageWidth === width && state.stageHeight === height && els.canvas.width === Math.floor(width * dpr) && els.canvas.height === Math.floor(height * dpr)) {
        return;
      }
      state.stageWidth = width;
      state.stageHeight = height;
      els.canvas.width = Math.floor(width * dpr);
      els.canvas.height = Math.floor(height * dpr);
      els.canvas.style.width = width + 'px';
      els.canvas.style.height = height + 'px';
    }

    function scheduleDraw() {
      if (state.drawFrame) return;
      state.drawFrame = window.requestAnimationFrame(function() {
        state.drawFrame = 0;
        draw();
      });
    }

    function scheduleResizeDraw() {
      if (state.resizedFrame) return;
      state.resizedFrame = window.requestAnimationFrame(function() {
        state.resizedFrame = 0;
        ensureCanvasSize();
        computeLayout(visibleNodes());
        if (!state.selected) fitToView(true);
        draw();
      });
    }

    function ingestGraph(data) {
      state.nodes = data.nodes || [];
      state.edges = data.edges || [];
      state.byId = {};
      state.children = {};
      state.parents = {};
      state.roots = [];
      state.visible = {};
      state.expanded = {};
      state.positions = {};
      state.details = {};
      state.selected = '';
      state.activeRoot = '';
      state.hovered = '';
      state.manualPositions = {};
      state.activity = loadActivity();

      state.nodes.forEach(function(node) {
        state.byId[node.id] = node;
        state.visible[node.id] = true;
      });
      state.edges.forEach(function(edge) {
        if (!state.children[edge.source_id]) state.children[edge.source_id] = [];
        if (!state.parents[edge.target_id]) state.parents[edge.target_id] = [];
        state.children[edge.source_id].push(edge.target_id);
        state.parents[edge.target_id].push(edge.source_id);
      });

      if (!els.actor.value) {
        els.actor.value = window.localStorage.getItem('raph-studio-actor') || '';
      }
      state.actor = actorName();
      window.localStorage.setItem('raph-studio-actor', state.actor);

      state.roots = state.nodes.filter(function(node) {
        return parentCount(node.id) === 0 || node.type === 'doc_site' || node.type === 'file';
      });
      state.roots.sort(function(a, b) {
        var ad = degreeOf(a.id);
        var bd = degreeOf(b.id);
        if (ad !== bd) return bd - ad;
        return nodeName(a).localeCompare(nodeName(b));
      });

      if (state.roots.length > 0) {
        focusRoot(state.roots[0].id, false);
      }
      ensureCanvasSize();
      computeLayout(visibleNodes());
      fitToView(true);
      updateStats();
      renderRoots();
      renderActivity();
      draw();
    }

    function updateStats() {
      var embedded = state.nodes.filter(function(node) { return (node.embedding_length || 0) > 0; }).length;
      var visible = Object.keys(state.visible).filter(function(id) {
        return state.byId[id];
      }).length;
      els.nodeCount.textContent = state.nodes.length;
      els.edgeCount.textContent = state.edges.length;
      els.visibleCount.textContent = visible;
      els.embedCount.textContent = embedded;
      if (state.selected && state.byId[state.selected]) {
        els.summary.textContent = visible + ' visible · focus ' + nodeName(state.byId[state.selected]);
      } else {
        els.summary.textContent = visible + ' visible of ' + state.nodes.length + ' nodes';
      }
      els.empty.hidden = state.nodes.length !== 0;
    }

    function renderRoots() {
      els.rootList.innerHTML = '';
      state.roots.slice(0, 80).forEach(function(root) {
        var btn = document.createElement('button');
        btn.className = 'root-button' + (state.activeRoot === root.id ? ' active' : '');
        btn.innerHTML =
          '<span class="dot ' + nodeKind(root) + '"></span>' +
          '<span class="root-name">' + escapeHTML(nodeName(root)) + '</span>' +
          '<span class="count-pill">' + degreeOf(root.id) + '</span>';
        btn.addEventListener('click', function() { focusRoot(root.id, true); });
        els.rootList.appendChild(btn);
      });
    }

    function focusRoot(id, shouldRender) {
      state.activeRoot = id;
      state.selected = id;
      state.hovered = '';
      resetVisibleToFocus(id);
      computeLayout(visibleNodes());
      fitToView(true);
      if (shouldRender) {
        renderRoots();
        updateStats();
        draw();
        showProperties(id);
        recordActivity('focus', id, 'anchor selected');
      }
    }

    function toggleNode(id) {
      if (!state.byId[id]) return;
      state.selected = id;
      state.activeRoot = id;
      state.hovered = '';
      resetVisibleToFocus(id);
      computeLayout(visibleNodes());
      fitToView(true);
      renderRoots();
      updateStats();
      draw();
      showProperties(id);
      recordActivity('select', id, 'node clicked');
    }

    function expand(id) {
      var next = {};
      [id].concat(neighborsOf(id)).forEach(function(nodeID) {
        next[nodeID] = true;
        neighborsOf(nodeID).forEach(function(nearID) {
          next[nearID] = true;
        });
      });
      Object.keys(next).forEach(function(nodeID) {
        state.visible[nodeID] = true;
      });
      state.expanded[id] = true;
      computeLayout(visibleNodes());
      fitToView(true);
      updateStats();
    }

    function computeLayout(list) {
      var width = state.stageWidth || 1200;
      var height = state.stageHeight || 720;
      var components = connectedComponents(list);
      var cols = Math.max(1, Math.ceil(Math.sqrt(components.length || 1)));
      var slotW = Math.max(420, width / cols);
      var rows = Math.max(1, Math.ceil((components.length || 1) / cols));
      var slotH = Math.max(320, height / rows);

      state.positions = {};
      components.forEach(function(component, index) {
        var row = Math.floor(index / cols);
        var col = index % cols;
        var centerX = col * slotW + slotW / 2;
        var centerY = row * slotH + slotH / 2;
        var allowed = {};
        component.forEach(function(id) { allowed[id] = true; });

        var anchor = component[0];
        if (state.selected && allowed[state.selected]) {
          anchor = state.selected;
        } else {
          anchor = component.slice().sort(function(a, b) {
            var da = degreeOf(a);
            var db = degreeOf(b);
            if (da !== db) return db - da;
            return nodeName(state.byId[a]).localeCompare(nodeName(state.byId[b]));
          })[0];
        }

        var distances = distanceMap(anchor, allowed);
        var layers = {};
        component.forEach(function(id) {
          var depth = distances[id] == null ? 4 : Math.min(distances[id], 4);
          if (!layers[depth]) layers[depth] = [];
          layers[depth].push(id);
        });

        state.positions[anchor] = {
          x: centerX,
          y: centerY,
          radius: radiusFor(anchor)
        };

        Object.keys(layers).map(Number).sort(function(a, b) { return a - b; }).forEach(function(depth) {
          if (depth === 0) return;
          var ring = layers[depth];
          var bandSize = depth === 1 ? 10 : depth === 2 ? 14 : 18;
          ring.forEach(function(id, idx) {
            if (id === anchor) return;
            var band = Math.floor(idx / bandSize);
            var within = idx % bandSize;
            var chunk = ring.slice(band * bandSize, band * bandSize + bandSize);
            var angleStep = (Math.PI * 2) / Math.max(1, chunk.length);
            var angle = within * angleStep - Math.PI / 2 + (band % 2) * 0.24 + hashAngle(id);
            var radius = 120 + depth * 96 + band * 76 + (degreeOf(id) * 3);
            state.positions[id] = {
              x: centerX + Math.cos(angle) * radius,
              y: centerY + Math.sin(angle) * radius,
              radius: radiusFor(id)
            };
          });
        });
      });

      Object.keys(state.manualPositions).forEach(function(id) {
        if (state.positions[id]) state.positions[id] = state.manualPositions[id];
      });
    }

    function hashAngle(id) {
      var sum = 0;
      for (var i = 0; i < id.length; i++) sum += id.charCodeAt(i);
      return ((sum % 21) - 10) * 0.012;
    }

    function radiusFor(id) {
      var degree = degreeOf(id);
      return Math.max(5, Math.min(17, 6 + Math.sqrt(degree * 2.8)));
    }

    function graphToScreen(point) {
      return {
        x: state.viewport.panX + point.x * state.viewport.scale,
        y: state.viewport.panY + point.y * state.viewport.scale
      };
    }

    function screenToGraph(x, y) {
      return {
        x: (x - state.viewport.panX) / state.viewport.scale,
        y: (y - state.viewport.panY) / state.viewport.scale
      };
    }

    function draw() {
      ensureCanvasSize();
      var width = state.stageWidth || 1;
      var height = state.stageHeight || 1;
      var dpr = window.devicePixelRatio || 1;
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      ctx.clearRect(0, 0, width, height);

      var list = visibleNodes();
      var visibleSet = {};
      list.forEach(function(node) { visibleSet[node.id] = true; });
      drawEdges(list, visibleSet);
      drawNodes(list);
      drawLabels(list);
      drawFocusChip();
      updateStats();
    }

    function edgeStroke(edge) {
      if (edge.type === 'DECLARES') return 'rgba(47, 121, 183, 0.42)';
      if (edge.type === 'LINKS_TO') return 'rgba(111, 120, 114, 0.24)';
      if (edge.type === 'HAS_SECTION') return 'rgba(45, 148, 102, 0.28)';
      return 'rgba(45, 148, 102, 0.34)';
    }

    function drawEdges(list, visibleSet) {
      var focus = focusID();
      ctx.save();
      ctx.translate(state.viewport.panX, state.viewport.panY);
      ctx.scale(state.viewport.scale, state.viewport.scale);
      state.edges.forEach(function(edge) {
        if (!visibleSet[edge.source_id] || !visibleSet[edge.target_id]) return;
        var source = graphPosition(edge.source_id);
        var target = graphPosition(edge.target_id);
        if (!source || !target) return;
        var related = !focus || (isRelatedToFocus(edge.source_id) && isRelatedToFocus(edge.target_id));
        var active = focus && (edge.source_id === focus || edge.target_id === focus || edge.source_id === state.selected || edge.target_id === state.selected);
        var alpha = active ? 1 : related ? 0.68 : 0.12;
        var bend = ((edge.source_id.length + edge.target_id.length) % 9) - 4;
        var cx = (source.x + target.x) / 2 + bend * 12;
        var cy = (source.y + target.y) / 2 - bend * 10;

        ctx.beginPath();
        ctx.moveTo(source.x, source.y);
        ctx.quadraticCurveTo(cx, cy, target.x, target.y);
        ctx.lineWidth = active ? 2 : edge.type === 'HAS_SECTION' ? 0.95 : 1.2;
        ctx.strokeStyle = edgeStroke(edge).replace(/[\d.]+\)$/, alpha + ')');
        ctx.stroke();
      });
      ctx.restore();
    }

    function drawNodes(list) {
      var focus = focusID();
      ctx.save();
      ctx.translate(state.viewport.panX, state.viewport.panY);
      ctx.scale(state.viewport.scale, state.viewport.scale);
      list.forEach(function(node) {
        var pos = graphPosition(node.id);
        if (!pos) return;
        var related = !focus || isRelatedToFocus(node.id);
        var active = node.id === state.selected || node.id === state.hovered;
        var radius = pos.radius || 7;

        if (active) {
          ctx.beginPath();
          ctx.fillStyle = 'rgba(40, 143, 97, 0.14)';
          ctx.arc(pos.x, pos.y, radius + 11, 0, Math.PI * 2);
          ctx.fill();
        }

        ctx.beginPath();
        ctx.fillStyle = nodeColor(node);
        ctx.globalAlpha = active ? 1 : related ? 0.86 : 0.26;
        ctx.arc(pos.x, pos.y, radius, 0, Math.PI * 2);
        ctx.fill();

        ctx.beginPath();
        ctx.globalAlpha = active ? 0.96 : related ? 0.52 : 0.16;
        ctx.strokeStyle = '#ffffff';
        ctx.lineWidth = active ? 2.6 : 1.4;
        ctx.arc(pos.x, pos.y, radius + 1.4, 0, Math.PI * 2);
        ctx.stroke();
        ctx.globalAlpha = 1;
      });
      ctx.restore();
    }

    function labelCandidates(list) {
      var picked = [];
      list.forEach(function(node) {
        if (node.id === state.selected || node.id === state.hovered || node.id === state.activeRoot) {
          picked.push(node);
          return;
        }
        if (degreeOf(node.id) >= 8) picked.push(node);
      });
      return picked.slice(0, 22);
    }

    function drawLabels(list) {
      var labels = labelCandidates(list);
      ctx.save();
      ctx.font = '12px "IBM Plex Sans", "Avenir Next", sans-serif';
      ctx.textBaseline = 'middle';
      labels.forEach(function(node) {
        var pos = graphPosition(node.id);
        if (!pos) return;
        var screen = graphToScreen(pos);
        var name = nodeName(node);
        var text = name.length > 28 ? name.slice(0, 26) + '…' : name;
        var width = ctx.measureText(text).width;
        var x = screen.x + (pos.radius || 7) * state.viewport.scale + 10;
        var y = screen.y;
        ctx.fillStyle = 'rgba(255,255,252,0.88)';
        ctx.fillRect(x - 6, y - 10, width + 12, 20);
        ctx.fillStyle = node.id === state.selected ? '#17211d' : '#44524a';
        ctx.fillText(text, x, y);
      });
      ctx.restore();
    }

    function drawFocusChip() {
      var id = focusID();
      if (!id || !state.byId[id]) {
        els.chip.classList.remove('visible');
        return;
      }
      var pos = graphPosition(id);
      if (!pos) {
        els.chip.classList.remove('visible');
        return;
      }
      var screen = graphToScreen(pos);
      els.chip.innerHTML =
        '<strong>' + escapeHTML(nodeName(state.byId[id])) + '</strong>' +
        '<small>' + escapeHTML(state.byId[id].type || 'node') + ' · ' + escapeHTML(state.byId[id].domain || 'memory') + '</small>' +
        '<span>' + degreeOf(id) + ' links · ' + shortID(id) + '</span>';
      var left = Math.min(state.stageWidth - 336, Math.max(14, screen.x + 18));
      var top = Math.min(state.stageHeight - 92, Math.max(14, screen.y - 16));
      els.chip.style.left = left + 'px';
      els.chip.style.top = top + 'px';
      els.chip.classList.add('visible');
    }

    function hitNode(clientX, clientY) {
      var rect = els.stage.getBoundingClientRect();
      var point = screenToGraph(clientX - rect.left, clientY - rect.top);
      var nodes = visibleNodes();
      var best = null;
      nodes.forEach(function(node) {
        var pos = graphPosition(node.id);
        if (!pos) return;
        var dx = point.x - pos.x;
        var dy = point.y - pos.y;
        var distance = Math.sqrt(dx * dx + dy * dy);
        var threshold = (pos.radius || 7) + 10 / Math.max(state.viewport.scale, 0.55);
        if (distance <= threshold && (!best || distance < best.distance)) {
          best = { id: node.id, distance: distance };
        }
      });
      return best ? best.id : '';
    }

    function fitToView(skipActivity) {
      var list = visibleNodes();
      if (!list.length) return;
      var bounds = {
        minX: Infinity,
        minY: Infinity,
        maxX: -Infinity,
        maxY: -Infinity
      };
      list.forEach(function(node) {
        var pos = graphPosition(node.id);
        if (!pos) return;
        bounds.minX = Math.min(bounds.minX, pos.x - pos.radius - 20);
        bounds.minY = Math.min(bounds.minY, pos.y - pos.radius - 20);
        bounds.maxX = Math.max(bounds.maxX, pos.x + pos.radius + 20);
        bounds.maxY = Math.max(bounds.maxY, pos.y + pos.radius + 20);
      });
      if (!isFinite(bounds.minX)) return;
      var width = Math.max(1, bounds.maxX - bounds.minX);
      var height = Math.max(1, bounds.maxY - bounds.minY);
      var pad = 64;
      var scaleX = (state.stageWidth - pad * 2) / width;
      var scaleY = (state.stageHeight - pad * 2) / height;
      state.viewport.scale = clampScale(Math.min(scaleX, scaleY, 1.6));
      state.viewport.panX = (state.stageWidth - width * state.viewport.scale) / 2 - bounds.minX * state.viewport.scale;
      state.viewport.panY = (state.stageHeight - height * state.viewport.scale) / 2 - bounds.minY * state.viewport.scale;
      draw();
      if (!skipActivity) recordActivity('fit', '', 'viewport fitted');
    }

    function clampScale(value) {
      return Math.max(0.22, Math.min(3.2, value));
    }

    function zoomAt(clientX, clientY, factor) {
      var rect = els.stage.getBoundingClientRect();
      var stageX = clientX - rect.left;
      var stageY = clientY - rect.top;
      var oldScale = state.viewport.scale;
      var nextScale = clampScale(oldScale * factor);
      if (nextScale === oldScale) return;
      var graphX = (stageX - state.viewport.panX) / oldScale;
      var graphY = (stageY - state.viewport.panY) / oldScale;
      state.viewport.scale = nextScale;
      state.viewport.panX = stageX - graphX * nextScale;
      state.viewport.panY = stageY - graphY * nextScale;
      scheduleDraw();
    }

    function zoomFromCenter(factor) {
      var rect = els.stage.getBoundingClientRect();
      zoomAt(rect.left + rect.width / 2, rect.top + rect.height / 2, factor);
    }

    function startStagePan(event) {
      if (event.button !== 0) return;
      var hit = hitNode(event.clientX, event.clientY);
      state.panMoved = false;
      if (hit) {
        var pos = graphPosition(hit);
        if (!pos) return;
        state.dragNode = hit;
        state.draggingNode = false;
        var rect = els.stage.getBoundingClientRect();
        var graphPoint = screenToGraph(event.clientX - rect.left, event.clientY - rect.top);
        state.dragOffsetX = graphPoint.x - pos.x;
        state.dragOffsetY = graphPoint.y - pos.y;
        els.stage.classList.add('dragging');
        event.preventDefault();
        return;
      }
      state.isPanning = true;
      state.panStartX = event.clientX;
      state.panStartY = event.clientY;
      state.panOriginX = state.viewport.panX;
      state.panOriginY = state.viewport.panY;
      els.stage.classList.add('panning');
      event.preventDefault();
    }

    function moveStagePan(event) {
      if (state.dragNode) {
        var rect = els.stage.getBoundingClientRect();
        var point = screenToGraph(event.clientX - rect.left, event.clientY - rect.top);
        var nextPos = {
          x: point.x - state.dragOffsetX,
          y: point.y - state.dragOffsetY,
          radius: graphPosition(state.dragNode).radius
        };
        var current = graphPosition(state.dragNode);
        var dx = nextPos.x - current.x;
        var dy = nextPos.y - current.y;
        if (!state.draggingNode && Math.abs(dx) + Math.abs(dy) > 2) state.draggingNode = true;
        if (!state.draggingNode) return;
        state.manualPositions[state.dragNode] = nextPos;
        state.panMoved = true;
        scheduleDraw();
        return;
      }

      if (!state.isPanning) {
        var hit = hitNode(event.clientX, event.clientY);
        if (hit !== state.hovered) {
          state.hovered = hit;
          scheduleDraw();
        }
        return;
      }

      var dx = event.clientX - state.panStartX;
      var dy = event.clientY - state.panStartY;
      if (Math.abs(dx) + Math.abs(dy) > 2) state.panMoved = true;
      state.viewport.panX = state.panOriginX + dx;
      state.viewport.panY = state.panOriginY + dy;
      scheduleDraw();
    }

    function stopStagePan() {
      if (state.dragNode) {
        if (state.draggingNode) {
          recordActivity('drag', state.dragNode, 'node repositioned');
        } else {
          toggleNode(state.dragNode);
        }
        state.dragNode = '';
        state.draggingNode = false;
        els.stage.classList.remove('dragging');
        return;
      }
      if (!state.isPanning) return;
      state.isPanning = false;
      els.stage.classList.remove('panning');
    }

    function renderTab(node, content, relationsHTML) {
      if (state.tab === 'relations') return '<div class="relation-list">' + relationsHTML + '</div>';
      if (state.tab === 'agent') {
        var payload = {
          id: node.id,
          domain: node.domain,
          type: node.type,
          name: node.name,
          url: node.url,
          path: node.path,
          embedding_length: node.embedding_length || 0,
          content_preview: String(node.content || '').slice(0, 1200)
        };
        return '<pre>' + escapeHTML(JSON.stringify(payload, null, 2)) + '</pre>';
      }
      return content ? '<pre>' + escapeHTML(content.slice(0, 5000)) + '</pre>' : '<div class="property wide"><label>Content</label><div>Empty</div></div>';
    }

    function renderTagList(tags) {
      if (!tags || !tags.length) return 'None';
      return tags.map(function(tag) { return escapeHTML(tag); }).join(', ');
    }

    function showProperties(id, skipFetch) {
      var node = state.details[id] || state.byId[id];
      if (!node) return;
      state.selected = id;
      els.properties.classList.add('visible');
      applyPanelState();
      els.panelTitle.textContent = nodeName(node);
      els.panelDot.className = 'dot ' + nodeKind(node);

      var outgoing = (state.children[id] || []).map(function(childID) {
        return { dir: 'child', type: edgeTypeBetween(id, childID), node: state.byId[childID] };
      }).filter(function(item) { return item.node; });
      var incoming = (state.parents[id] || []).map(function(parentID) {
        return { dir: 'parent', type: edgeTypeBetween(parentID, id), node: state.byId[parentID] };
      }).filter(function(item) { return item.node; });

      var content = node.content || '';
      var memory = node.memory || null;
      var webCorpus = node.web_corpus || null;
      var webCrawlVersion = node.web_crawl_version || null;
      var relationsHTML = incoming.concat(outgoing).slice(0, 80).map(function(item) {
        return '<div class="relation" data-id="' + escapeHTML(item.node.id) + '">' +
          '<span>' + escapeHTML(item.dir) + '<br>' + escapeHTML(item.type) + '</span>' +
          '<div><strong>' + escapeHTML(nodeName(item.node)) + '</strong><small>' + escapeHTML(item.node.type || '') + ' · ' + escapeHTML(shortID(item.node.id)) + '</small></div>' +
        '</div>';
      }).join('');
      if (!relationsHTML) relationsHTML = '<div class="property wide"><label>Relations</label><div>None</div></div>';

      var memoryHTML = memory ? '' +
        '<div class="property"><label>Scope</label><div>' + escapeHTML(memory.scope_type || '') + ' · ' + escapeHTML(memory.scope_id || '') + '</div></div>' +
        '<div class="property"><label>Knowledge</label><div>' + escapeHTML(memory.knowledge_type || '') + '</div></div>' +
        '<div class="property"><label>Lifecycle</label><div>' + escapeHTML(memory.lifecycle_state || '') + '</div></div>' +
        '<div class="property"><label>Revision</label><div>' + escapeHTML(memory.revision || 0) + '</div></div>' +
        '<div class="property"><label>Source</label><div>' + escapeHTML(memory.source || '') + '</div></div>' +
        '<div class="property"><label>Writer</label><div>' + escapeHTML(memory.writer_id || '') + '</div></div>' +
        '<div class="property wide"><label>Memory Key</label><div>' + escapeHTML(memory.memory_key || '') + '</div></div>' +
        '<div class="property wide"><label>Display Tags</label><div>' + renderTagList(memory.display_tags) + '</div></div>' +
        '<div class="property wide"><label>Normalized Tags</label><div>' + renderTagList(memory.normalized_tags) + '</div></div>' +
        '<div class="property"><label>Created</label><div>' + escapeHTML(memory.created_at || '') + '</div></div>' +
        '<div class="property"><label>Updated</label><div>' + escapeHTML(memory.updated_at || '') + '</div></div>' +
        (memory.replaced_by_node_id ? '<div class="property wide"><label>Replaced By</label><div>' + escapeHTML(memory.replaced_by_node_id) + '</div></div>' : '') +
        (memory.deprecated_message ? '<div class="property wide"><label>Deprecation</label><div>' + escapeHTML(memory.deprecated_message) + '</div></div>' : '')
        : '';

      var webHTML = webCorpus ? '' +
        '<div class="property"><label>Corpus Scope</label><div>' + escapeHTML(webCorpus.scope_type || '') + ' · ' + escapeHTML(webCorpus.scope_id || '') + '</div></div>' +
        '<div class="property"><label>Corpus Source</label><div>' + escapeHTML(webCorpus.source || '') + '</div></div>' +
        '<div class="property wide"><label>Corpus ID</label><div>' + escapeHTML(webCorpus.id || '') + '</div></div>' +
        '<div class="property wide"><label>Base URL</label><div>' + escapeHTML(webCorpus.base_url || '') + '</div></div>' +
        '<div class="property"><label>Corpus Created</label><div>' + escapeHTML(webCorpus.created_at || '') + '</div></div>' +
        '<div class="property"><label>Corpus Updated</label><div>' + escapeHTML(webCorpus.updated_at || '') + '</div></div>' +
        (webCrawlVersion ? '<div class="property wide"><label>Latest Crawl</label><div>' + escapeHTML(webCrawlVersion.id || '') + '</div></div>' : '') +
        (webCrawlVersion ? '<div class="property wide"><label>Seed URL</label><div>' + escapeHTML(webCrawlVersion.seed_url || '') + '</div></div>' : '') +
        (webCrawlVersion ? '<div class="property"><label>Crawled At</label><div>' + escapeHTML(webCrawlVersion.created_at || '') + '</div></div>' : '')
        : '';

      els.propertiesBody.innerHTML =
        '<div class="property-grid">' +
          '<div class="property"><label>Domain</label><div>' + escapeHTML(node.domain || '') + '</div></div>' +
          '<div class="property"><label>Type</label><div>' + escapeHTML(node.type || '') + '</div></div>' +
          '<div class="property"><label>Embedding</label><div>' + escapeHTML(node.embedding_length || 0) + ' floats</div></div>' +
          '<div class="property"><label>Edges</label><div>' + incoming.length + ' in · ' + outgoing.length + ' out</div></div>' +
          '<div class="property wide"><label>ID</label><div>' + escapeHTML(node.id || '') + '</div></div>' +
          (node.path ? '<div class="property wide"><label>Codebase path</label><div>' + escapeHTML(node.path) + '</div></div>' : '') +
          (node.url ? '<div class="property wide"><label>URL</label><div>' + escapeHTML(node.url) + '</div></div>' : '') +
          memoryHTML +
          webHTML +
        '</div>' +
        '<div class="tabs">' +
          '<button data-tab="content" class="' + (state.tab === 'content' ? 'active' : '') + '">Content</button>' +
          '<button data-tab="relations" class="' + (state.tab === 'relations' ? 'active' : '') + '">Relations</button>' +
          '<button data-tab="agent" class="' + (state.tab === 'agent' ? 'active' : '') + '">Agent JSON</button>' +
        '</div>' +
        '<div id="tab-content">' + renderTab(node, content, relationsHTML) + '</div>' +
        '<div class="actions">' +
          '<button id="expand-selected">Expand</button>' +
          '<button id="focus-selected">Focus</button>' +
          '<button id="delete-selected" class="danger">Delete</button>' +
        '</div>';

      els.propertiesBody.querySelectorAll('.tabs button').forEach(function(button) {
        button.addEventListener('click', function() {
          state.tab = button.dataset.tab;
          showProperties(id, true);
        });
      });
      els.propertiesBody.querySelectorAll('.relation').forEach(function(rel) {
        rel.addEventListener('click', function() {
          var nextID = rel.dataset.id;
          state.selected = nextID;
          state.hovered = '';
          resetVisibleToFocus(nextID);
          computeLayout(visibleNodes());
          fitToView(true);
          renderRoots();
          draw();
          showProperties(nextID);
          recordActivity('focus', nextID, 'relation opened');
        });
      });
      document.getElementById('expand-selected').addEventListener('click', function() {
        expand(id);
        draw();
        showProperties(id, true);
      });
      document.getElementById('focus-selected').addEventListener('click', function() {
        focusRoot(id, true);
      });
      document.getElementById('delete-selected').addEventListener('click', function() {
        deleteNode(id);
      });

      if (!skipFetch && !state.details[id]) {
        fetch('/api/node?id=' + encodeURIComponent(id)).then(function(res) {
          if (!res.ok) return null;
          return res.json();
        }).then(function(fullNode) {
          if (!fullNode) return;
          state.details[id] = fullNode;
          state.byId[id] = fullNode;
          if (state.selected === id) {
            showProperties(id, true);
            scheduleDraw();
          }
        }).catch(function() {});
      }
      scheduleDraw();
    }

    async function deleteNode(id) {
      var res = await fetch('/api/node/delete', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: id })
      });
      if (!res.ok) {
        toast('Delete failed: ' + await res.text());
        return;
      }
      els.properties.classList.remove('visible');
      toast('Node deleted');
      recordActivity('delete', id, 'node removed');
      reloadGraph();
    }

    async function reloadGraph() {
      els.summary.textContent = 'Loading';
      var res = await fetch('/api/graph');
      if (!res.ok) {
        els.summary.textContent = 'Load failed';
        toast(await res.text());
        return;
      }
      recordActivity('reload', '', 'graph refreshed');
      ingestGraph(await res.json());
      loadSQLiteData();
    }

    async function loadSQLiteData() {
      var res = await fetch('/api/sqlite?limit=250');
      if (!res.ok) {
        els.sqliteWrap.innerHTML = '<div class="sqlite-empty">Load failed.</div>';
        return;
      }
      var payload = await res.json();
      state.sqliteTables = payload.tables || [];
      renderSQLite();
    }

    async function runAgentSearch() {
      var query = els.agentQuery.value.trim();
      if (!query) return;
      els.agentOutput.textContent = 'Searching...';
      var res = await fetch('/api/search', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ query: query, limit: 10 })
      });
      var text = res.ok ? JSON.stringify(await res.json(), null, 2) : await res.text();
      els.agentOutput.textContent = text;
      if (res.ok) {
        var data = JSON.parse(text);
        focusMatches(data.matches || []);
      }
      recordActivity('search', '', query);
    }

    async function runAgentNeighbors() {
      var id = state.selected || els.agentQuery.value.trim();
      if (!id) return;
      els.agentOutput.textContent = 'Loading neighbors...';
      var res = await fetch('/api/neighbors', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ node_id: id })
      });
      els.agentOutput.textContent = res.ok ? JSON.stringify(await res.json(), null, 2) : await res.text();
      recordActivity('neighbors', id, 'neighbor lookup');
    }

    function focusMatches(matches) {
      matches.forEach(function(node) {
        state.visible[node.id] = true;
      });
      if (matches[0]) {
        state.selected = matches[0].id;
        state.hovered = '';
        resetVisibleToFocus(matches[0].id);
        computeLayout(visibleNodes());
        fitToView(true);
        renderRoots();
        showProperties(matches[0].id);
        recordActivity('result', matches[0].id, 'top search match');
      }
      scheduleDraw();
    }

    function toast(message) {
      els.toast.textContent = message;
      els.toast.classList.add('visible');
      window.clearTimeout(toast.timer);
      toast.timer = window.setTimeout(function() {
        els.toast.classList.remove('visible');
      }, 2600);
    }

    function setActionBusy(busy, label) {
      els.initDemo.disabled = busy;
      els.clearDB.disabled = busy;
      els.reload.disabled = busy;
      if (busy) {
        els.summary.textContent = label || 'Working';
      }
    }

    async function clearDatabase() {
      if (!window.confirm('Clear the local raph database?')) return;
      setActionBusy(true, 'Clearing database...');
      var res = await fetch('/api/actions/clear', { method: 'POST' });
      var text = res.ok ? '' : await res.text();
      setActionBusy(false, '');
      if (!res.ok) {
        toast('Clear failed: ' + text);
        return;
      }
      toast('Local database cleared');
      recordActivity('clear', '', 'database wiped');
      reloadGraph();
    }

    async function initDemo() {
      if (!window.confirm('Clear the database, index this workspace, and crawl example.com?')) return;
      setActionBusy(true, 'Indexing workspace and crawling example.com...');
      var res = await fetch('/api/actions/init', { method: 'POST' });
      var text = await res.text();
      setActionBusy(false, '');
      if (!res.ok) {
        toast('Init failed: ' + text);
        return;
      }
      var data = {};
      try {
        data = JSON.parse(text);
      } catch (error) {}
      toast('Studio init finished');
      recordActivity('init', '', (data.workspace_root || 'workspace') + ' + ' + (data.seed_url || 'seed'));
      reloadGraph();
    }

    function makeDraggable(panel, handle) {
      var dragging = false;
      var offsetX = 0;
      var offsetY = 0;
      handle.addEventListener('mousedown', function(event) {
        if (event.target.tagName === 'BUTTON') return;
        var rect = panel.getBoundingClientRect();
        dragging = true;
        offsetX = event.clientX - rect.left;
        offsetY = event.clientY - rect.top;
        panel.style.right = 'auto';
      });
      document.addEventListener('mousemove', function(event) {
        if (!dragging) return;
        panel.style.left = Math.max(8, event.clientX - offsetX) + 'px';
        panel.style.top = Math.max(72, event.clientY - offsetY) + 'px';
      });
      document.addEventListener('mouseup', function() { dragging = false; });
    }

    els.reload.addEventListener('click', reloadGraph);
    els.fit.addEventListener('click', function() { fitToView(false); });
    els.zoomOut.addEventListener('click', function() { zoomFromCenter(0.82); });
    els.zoomIn.addEventListener('click', function() { zoomFromCenter(1.22); });
    els.initDemo.addEventListener('click', initDemo);
    els.clearDB.addEventListener('click', clearDatabase);
    els.panelClose.addEventListener('click', function() { els.properties.classList.remove('visible'); });
    els.panelCollapse.addEventListener('click', function() {
      state.panelCollapsed = !state.panelCollapsed;
      applyPanelState();
    });
    els.panelExpand.addEventListener('click', function() {
      state.panelExpanded = !state.panelExpanded;
      applyPanelState();
    });
    els.filter.addEventListener('input', function() {
      state.filter = els.filter.value.trim().toLowerCase();
      computeLayout(visibleNodes());
      fitToView(true);
      scheduleDraw();
    });
    els.actor.value = window.localStorage.getItem('raph-studio-actor') || '';
    els.actor.addEventListener('change', function() {
      state.actor = actorName();
      window.localStorage.setItem('raph-studio-actor', state.actor);
      recordActivity('actor', '', 'tag set to ' + state.actor);
    });
    els.stage.addEventListener('wheel', function(event) {
      event.preventDefault();
      zoomAt(event.clientX, event.clientY, event.deltaY > 0 ? 0.88 : 1.14);
    }, { passive: false });
    els.stage.addEventListener('mousedown', startStagePan);
    els.stage.addEventListener('mouseleave', function() {
      if (!state.isPanning && !state.dragNode && state.hovered) {
        state.hovered = '';
        scheduleDraw();
      }
    });
    document.addEventListener('mousemove', moveStagePan);
    document.addEventListener('mouseup', stopStagePan);
    els.stage.addEventListener('click', function(event) {
      if (state.panMoved) {
        state.panMoved = false;
        return;
      }
      var hit = hitNode(event.clientX, event.clientY);
      if (hit) {
        toggleNode(hit);
        return;
      }
      state.selected = '';
      state.hovered = '';
      els.properties.classList.remove('visible');
      scheduleDraw();
    });
    els.agentSearch.addEventListener('click', runAgentSearch);
    els.agentNeighbors.addEventListener('click', runAgentNeighbors);
    els.agentQuery.addEventListener('keydown', function(event) {
      if (event.key === 'Enter') runAgentSearch();
    });
    els.navButtons.forEach(function(button) {
      button.addEventListener('click', function() {
        setView(button.dataset.view);
      });
    });
    window.addEventListener('resize', scheduleResizeDraw);
    makeDraggable(els.properties, els.propertiesHead);
    applyPanelState();
    setView('graph');
    reloadGraph();
  </script>
</body>
</html>
`
