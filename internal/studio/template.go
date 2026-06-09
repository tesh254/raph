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
      --bg: #f6f7f3;
      --ink: #1d211f;
      --muted: #69706c;
      --line: #d9ded6;
      --panel: rgba(255, 255, 252, 0.94);
      --code: #2368a2;
      --doc: #2f855a;
      --memory: #7b4aa0;
      --warn: #c24135;
      --shadow: 0 18px 48px rgba(38, 47, 42, 0.14);
    }
    html, body { height: 100%; }
    body {
      margin: 0;
      color: var(--ink);
      background:
        linear-gradient(rgba(255,255,255,0.62), rgba(255,255,255,0.62)),
        radial-gradient(#d5dbd2 1px, transparent 1px);
      background-size: auto, 22px 22px;
      font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      overflow: hidden;
    }
    button, input {
      font: inherit;
    }
    button {
      border: 1px solid var(--line);
      border-radius: 8px;
      background: #fff;
      color: var(--ink);
      min-height: 34px;
      padding: 0 12px;
      cursor: pointer;
    }
    button:hover { border-color: #abb6ad; }
    button.primary {
      background: #203029;
      border-color: #203029;
      color: #fff;
    }
    button.ghost {
      background: transparent;
    }
    button.danger {
      background: #fff4f2;
      border-color: #edc7c0;
      color: var(--warn);
    }
    input {
      width: 100%;
      min-height: 36px;
      border: 1px solid var(--line);
      border-radius: 8px;
      padding: 0 11px;
      color: var(--ink);
      background: #fff;
      outline: none;
    }
    input:focus { border-color: #8fa798; box-shadow: 0 0 0 3px rgba(47, 133, 90, 0.12); }
    pre {
      margin: 0;
      max-height: 260px;
      overflow: auto;
      white-space: pre-wrap;
      word-break: break-word;
      border: 1px solid var(--line);
      border-radius: 8px;
      background: #f7f8f5;
      padding: 12px;
      font: 12px/1.55 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    }
    #app {
      position: fixed;
      inset: 0;
      display: grid;
      grid-template-columns: 292px 1fr;
      grid-template-rows: 64px 1fr;
    }
    #topbar {
      grid-column: 1 / 3;
      display: flex;
      align-items: center;
      gap: 12px;
      padding: 12px 16px;
      border-bottom: 1px solid rgba(42, 50, 45, 0.1);
      background: rgba(252, 253, 249, 0.86);
      backdrop-filter: blur(14px);
      z-index: 5;
    }
    .brand {
      width: 132px;
      font-weight: 760;
      letter-spacing: 0;
    }
    .brand span { color: var(--doc); }
    .searchbox { flex: 1; max-width: 520px; }
    #summary {
      color: var(--muted);
      font-size: 13px;
      white-space: nowrap;
    }
    #sidebar {
      border-right: 1px solid rgba(42, 50, 45, 0.1);
      background: rgba(250, 251, 247, 0.76);
      padding: 14px;
      overflow: auto;
    }
    .section-title {
      margin: 14px 0 8px;
      color: var(--muted);
      font-size: 11px;
      font-weight: 780;
      text-transform: uppercase;
      letter-spacing: 0.08em;
    }
    .section-title:first-child { margin-top: 0; }
    .metric-grid {
      display: grid;
      grid-template-columns: 1fr 1fr;
      gap: 8px;
    }
    .metric {
      border: 1px solid var(--line);
      border-radius: 8px;
      background: rgba(255,255,255,0.74);
      padding: 9px;
    }
    .metric strong { display: block; font-size: 20px; line-height: 1.1; }
    .metric span { color: var(--muted); font-size: 12px; }
    .root-list {
      display: grid;
      gap: 7px;
    }
    .root-button {
      width: 100%;
      min-height: 40px;
      padding: 8px 10px;
      display: grid;
      grid-template-columns: 9px 1fr auto;
      align-items: center;
      gap: 8px;
      text-align: left;
      background: rgba(255,255,255,0.72);
    }
    .root-button.active {
      border-color: #8caf99;
      background: #f2faf4;
    }
    .dot { width: 9px; height: 9px; border-radius: 999px; background: var(--memory); }
    .dot.code { background: var(--code); }
    .dot.documentation { background: var(--doc); }
    .dot.memory { background: var(--memory); }
    .root-name {
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
      font-size: 13px;
      font-weight: 650;
    }
    .count-pill {
      border-radius: 999px;
      padding: 2px 7px;
      background: #eef1eb;
      color: var(--muted);
      font-size: 11px;
    }
    .agent-card {
      border: 1px solid var(--line);
      border-radius: 8px;
      background: rgba(255,255,255,0.78);
      padding: 10px;
    }
    .agent-card .row {
      display: flex;
      gap: 8px;
      margin-top: 8px;
    }
    .agent-card .row button { flex: 0 0 auto; }
    .agent-card pre {
      margin-top: 8px;
      max-height: 220px;
      background: #fbfbf8;
    }
    #stage {
      position: relative;
      overflow: hidden;
      cursor: grab;
      touch-action: none;
    }
    #stage.panning {
      cursor: grabbing;
    }
    #edges {
      position: absolute;
      inset: 0;
      width: 100%;
      height: 100%;
      pointer-events: none;
    }
    #nodes {
      position: absolute;
      inset: 0;
      transform-origin: 0 0;
    }
    .node-card {
      position: absolute;
      width: 230px;
      min-height: 72px;
      border: 1px solid #cdd5cf;
      border-radius: 8px;
      background: rgba(255,255,252,0.94);
      box-shadow: 0 8px 24px rgba(35, 42, 38, 0.1);
      padding: 10px;
      cursor: pointer;
      user-select: none;
      transition: border-color 120ms ease, box-shadow 120ms ease, transform 120ms ease;
    }
    .node-card:hover {
      border-color: #8fa798;
      box-shadow: var(--shadow);
      transform: translateY(-1px);
    }
    .node-card.selected {
      border-color: #203029;
      box-shadow: 0 0 0 3px rgba(32, 48, 41, 0.12), var(--shadow);
    }
    .node-head {
      display: flex;
      align-items: center;
      gap: 8px;
      min-width: 0;
    }
    .node-title {
      flex: 1;
      min-width: 0;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
      font-weight: 720;
      font-size: 13px;
    }
    .node-type {
      margin-top: 7px;
      display: flex;
      align-items: center;
      gap: 6px;
      color: var(--muted);
      font-size: 12px;
    }
    .expand-pill {
      margin-left: auto;
      border-radius: 999px;
      background: #eef1eb;
      color: #4e5c55;
      padding: 2px 7px;
      font-size: 11px;
      line-height: 1.3;
    }
    .node-snippet {
      margin-top: 7px;
      color: #525d57;
      font-size: 12px;
      line-height: 1.35;
      display: -webkit-box;
      -webkit-line-clamp: 2;
      -webkit-box-orient: vertical;
      overflow: hidden;
    }
    .edge-label {
      fill: #68736c;
      font: 11px ui-sans-serif, system-ui, sans-serif;
      paint-order: stroke;
      stroke: rgba(246,247,243,0.9);
      stroke-width: 5px;
      stroke-linejoin: round;
    }
    .empty-state {
      position: absolute;
      left: 50%;
      top: 50%;
      transform: translate(-50%, -50%);
      max-width: 420px;
      border: 1px solid var(--line);
      border-radius: 8px;
      background: var(--panel);
      padding: 18px;
      box-shadow: var(--shadow);
      color: var(--muted);
      text-align: center;
    }
    .empty-state strong {
      display: block;
      color: var(--ink);
      margin-bottom: 6px;
    }
    #properties {
      position: fixed;
      top: 82px;
      right: 18px;
      width: min(440px, calc(100vw - 36px));
      max-height: calc(100vh - 106px);
      overflow: hidden;
      display: none;
      flex-direction: column;
      border: 1px solid rgba(32, 48, 41, 0.14);
      border-radius: 8px;
      background: var(--panel);
      box-shadow: var(--shadow);
      backdrop-filter: blur(16px);
      z-index: 8;
    }
    #properties.visible { display: flex; }
    .panel-head {
      display: flex;
      align-items: center;
      gap: 10px;
      padding: 13px 14px;
      border-bottom: 1px solid var(--line);
      cursor: grab;
    }
    .panel-title {
      min-width: 0;
      flex: 1;
      font-weight: 760;
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
      gap: 8px;
      margin-bottom: 12px;
    }
    .property {
      border: 1px solid var(--line);
      border-radius: 8px;
      padding: 8px;
      background: rgba(255,255,255,0.62);
      min-width: 0;
    }
    .property label {
      display: block;
      color: var(--muted);
      font-size: 11px;
      font-weight: 720;
      text-transform: uppercase;
      letter-spacing: 0.06em;
      margin-bottom: 4px;
    }
    .property div {
      overflow-wrap: anywhere;
      font-size: 13px;
    }
    .wide { grid-column: 1 / -1; }
    .tabs {
      display: flex;
      gap: 6px;
      margin: 12px 0 10px;
    }
    .tabs button {
      flex: 1;
      min-height: 32px;
      font-size: 13px;
      background: #f5f6f1;
    }
    .tabs button.active {
      background: #203029;
      border-color: #203029;
      color: #fff;
    }
    .relation-list {
      display: grid;
      gap: 6px;
      margin-bottom: 12px;
    }
    .relation {
      display: grid;
      grid-template-columns: 72px 1fr;
      gap: 8px;
      align-items: start;
      border: 1px solid var(--line);
      border-radius: 8px;
      padding: 8px;
      background: #fff;
      cursor: pointer;
    }
    .relation span:first-child {
      color: var(--muted);
      font-size: 11px;
      font-weight: 740;
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
      padding: 8px 14px;
      color: var(--muted);
      font-size: 13px;
      display: none;
      z-index: 10;
    }
    .toast.visible { display: block; }
    @media (max-width: 860px) {
      #app {
        grid-template-columns: 1fr;
        grid-template-rows: auto 172px 1fr;
      }
      #topbar { grid-column: 1; flex-wrap: wrap; }
      .brand { width: auto; }
      .searchbox { order: 3; flex-basis: 100%; max-width: none; }
      #sidebar {
        grid-row: 2;
        border-right: 0;
        border-bottom: 1px solid rgba(42, 50, 45, 0.1);
        display: grid;
        grid-template-columns: 160px 1fr;
        gap: 12px;
      }
      #stage { grid-row: 3; }
      .metric-grid { grid-template-columns: 1fr 1fr; }
      .root-list { grid-template-columns: repeat(auto-fit, minmax(160px, 1fr)); }
      .agent-card { display: none; }
    }
  </style>
</head>
<body>
  <div id="app">
    <div id="topbar">
      <div class="brand">raph<span>Studio</span></div>
      <div class="searchbox"><input id="filter" type="search" placeholder="Filter knowledge"></div>
      <button id="fit">Fit</button>
      <button id="zoom-out">-</button>
      <button id="zoom-in">+</button>
      <button id="reload" class="primary">Reload</button>
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

      <div>
        <div class="section-title">Parents</div>
        <div id="root-list" class="root-list"></div>
      </div>

      <div class="agent-card">
        <div class="section-title">Agent View</div>
        <input id="agent-query" type="search" placeholder="Query MCP search">
        <div class="row">
          <button id="agent-search" class="primary">Search</button>
          <button id="agent-neighbors">Neighbors</button>
        </div>
        <pre id="agent-output">No query yet.</pre>
      </div>
    </aside>

    <main id="stage">
      <svg id="edges" aria-hidden="true"></svg>
      <div id="nodes"></div>
      <div id="empty" class="empty-state" hidden>
        <strong>No graph data yet</strong>
        <span>Run raph init or raph crawl, then reload Studio.</span>
      </div>
    </main>
  </div>

  <section id="properties" aria-live="polite">
    <div id="properties-head" class="panel-head">
      <span class="dot" id="panel-dot"></span>
      <div id="panel-title" class="panel-title">Node</div>
      <button id="panel-close" class="ghost">Close</button>
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
      filter: '',
      details: {},
      positions: {},
      scale: 1,
      panX: 0,
      panY: 0,
      isPanning: false,
      panStartX: 0,
      panStartY: 0,
      panOriginX: 0,
      panOriginY: 0,
      panMoved: false,
      tab: 'content'
    };

    var els = {
      stage: document.getElementById('stage'),
      nodes: document.getElementById('nodes'),
      edges: document.getElementById('edges'),
      empty: document.getElementById('empty'),
      filter: document.getElementById('filter'),
      reload: document.getElementById('reload'),
      fit: document.getElementById('fit'),
      zoomOut: document.getElementById('zoom-out'),
      zoomIn: document.getElementById('zoom-in'),
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
      panelClose: document.getElementById('panel-close'),
      agentQuery: document.getElementById('agent-query'),
      agentSearch: document.getElementById('agent-search'),
      agentNeighbors: document.getElementById('agent-neighbors'),
      agentOutput: document.getElementById('agent-output'),
      toast: document.getElementById('toast')
    };

	function escapeHTML(value) {
		return String(value == null ? '' : value).replace(/[&<>"']/g, function(ch) {
			return ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'})[ch];
		});
	}

    function shortID(id) {
      id = String(id || '');
      return id.length > 22 ? id.slice(0, 10) + '...' + id.slice(-8) : id;
    }

    function nodeName(node) {
      if (!node) return '';
      return node.name || node.url || node.id;
    }

    function nodeKind(node) {
      if (!node) return 'memory';
      return node.domain === 'code' ? 'code' : node.domain === 'documentation' ? 'documentation' : 'memory';
    }

    function childCount(id) {
      return (state.children[id] || []).length;
    }

    function parentCount(id) {
      return (state.parents[id] || []).length;
    }

    function edgeTypeBetween(source, target) {
      for (var i = 0; i < state.edges.length; i++) {
        var e = state.edges[i];
        if (e.source_id === source && e.target_id === target) return e.type;
      }
      return '';
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

      state.nodes.forEach(function(node) {
        state.byId[node.id] = node;
      });
      state.edges.forEach(function(edge) {
        if (!state.children[edge.source_id]) state.children[edge.source_id] = [];
        if (!state.parents[edge.target_id]) state.parents[edge.target_id] = [];
        state.children[edge.source_id].push(edge.target_id);
        state.parents[edge.target_id].push(edge.source_id);
      });

      state.roots = state.nodes.filter(function(node) {
        return parentCount(node.id) === 0 || node.type === 'doc_site' || node.type === 'file';
      });
      state.roots.sort(function(a, b) {
        var ap = childCount(a.id);
        var bp = childCount(b.id);
        if (ap !== bp) return bp - ap;
        return nodeName(a).localeCompare(nodeName(b));
      });

      if (state.roots.length > 0) {
        focusRoot(state.roots[0].id, false);
      }
      updateStats();
      renderRoots();
      render();
    }

    function updateStats() {
      var embedded = state.nodes.filter(function(n) { return (n.embedding_length || 0) > 0; }).length;
      var visible = Object.keys(state.visible).length;
      els.nodeCount.textContent = state.nodes.length;
      els.edgeCount.textContent = state.edges.length;
      els.visibleCount.textContent = visible;
      els.embedCount.textContent = embedded;
      els.summary.textContent = visible + ' visible of ' + state.nodes.length + ' nodes';
    }

    function renderRoots() {
      els.rootList.innerHTML = '';
      state.roots.slice(0, 80).forEach(function(root) {
        var btn = document.createElement('button');
        btn.className = 'root-button' + (state.activeRoot === root.id ? ' active' : '');
        btn.innerHTML =
          '<span class="dot ' + nodeKind(root) + '"></span>' +
          '<span class="root-name">' + escapeHTML(nodeName(root)) + '</span>' +
          '<span class="count-pill">' + childCount(root.id) + '</span>';
        btn.addEventListener('click', function() { focusRoot(root.id, true); });
        els.rootList.appendChild(btn);
      });
    }

    function focusRoot(id, shouldRender) {
      state.activeRoot = id;
      state.visible = {};
      state.expanded = {};
      state.visible[id] = true;
      state.expanded[id] = true;
      (state.children[id] || []).forEach(function(childID) {
        state.visible[childID] = true;
      });
      if (shouldRender) {
        state.selected = id;
        renderRoots();
        render();
        showProperties(id);
      }
    }

    function toggleNode(id) {
      if (!state.byId[id]) return;
      state.selected = id;
      if (childCount(id) > 0) {
        if (state.expanded[id]) {
          collapse(id);
        } else {
          expand(id);
        }
      }
      render();
      showProperties(id);
    }

    function expand(id) {
      state.visible[id] = true;
      state.expanded[id] = true;
      (state.children[id] || []).forEach(function(childID) {
        state.visible[childID] = true;
      });
    }

    function collapse(id) {
      delete state.expanded[id];
      getDescendants(id).forEach(function(descID) {
        delete state.expanded[descID];
        delete state.visible[descID];
      });
      state.visible[id] = true;
    }

    function getDescendants(id) {
      var result = [];
      var queue = (state.children[id] || []).slice();
      while (queue.length) {
        var next = queue.shift();
        result.push(next);
        (state.children[next] || []).forEach(function(childID) {
          queue.push(childID);
        });
      }
      return result;
    }

    function visibleNodes() {
      var list = state.nodes.filter(function(node) {
        if (!state.visible[node.id]) return false;
        if (!state.filter) return true;
        var haystack = [
          node.id, node.name, node.type, node.domain, node.url, node.content
        ].join(' ').toLowerCase();
        return haystack.indexOf(state.filter) !== -1;
      });
      list.sort(function(a, b) {
        var da = depthOf(a.id);
        var db = depthOf(b.id);
        if (da !== db) return da - db;
        return nodeName(a).localeCompare(nodeName(b));
      });
      return list;
    }

    function depthOf(id) {
      var depth = 0;
      var seen = {};
      var current = id;
      while (state.parents[current] && state.parents[current].length && !seen[current]) {
        seen[current] = true;
        var parent = state.parents[current][0];
        if (!state.visible[parent]) break;
        depth++;
        current = parent;
      }
      return depth;
    }

    function computeLayout(list) {
      var stageRect = els.stage.getBoundingClientRect();
      var columns = {};
      list.forEach(function(node) {
        var d = Math.min(depthOf(node.id), 6);
        if (!columns[d]) columns[d] = [];
        columns[d].push(node);
      });
      var colKeys = Object.keys(columns).map(Number).sort(function(a, b) { return a - b; });
      var cardW = 230;
      var colGap = 76;
      var rowGap = 24;
      var maxRows = 0;
      colKeys.forEach(function(k) { maxRows = Math.max(maxRows, columns[k].length); });
      var contentW = Math.max(stageRect.width - 40, colKeys.length * (cardW + colGap) + 80);
      var contentH = Math.max(stageRect.height - 40, maxRows * (96 + rowGap) + 80);

      colKeys.forEach(function(k, colIndex) {
        var nodes = columns[k];
        var x = 40 + colIndex * (cardW + colGap);
        var totalH = nodes.length * 96 + Math.max(nodes.length - 1, 0) * rowGap;
        var y = Math.max(40, (contentH - totalH) / 2);
        nodes.forEach(function(node, rowIndex) {
          state.positions[node.id] = { x: x, y: y + rowIndex * (96 + rowGap), w: cardW, h: 82 };
        });
      });

      if (state.scale === 1 && state.panX === 0 && state.panY === 0) {
        state.panX = Math.max(16, (stageRect.width - Math.min(contentW, stageRect.width)) / 2);
        state.panY = 18;
      }
      els.edges.setAttribute('viewBox', '0 0 ' + stageRect.width + ' ' + stageRect.height);
      return { width: contentW, height: contentH };
    }

    function applyTransform() {
      els.nodes.style.transform = 'translate(' + state.panX + 'px,' + state.panY + 'px) scale(' + state.scale + ')';
    }

    function clampScale(value) {
      return Math.max(0.35, Math.min(2.8, value));
    }

    function zoomAt(clientX, clientY, factor) {
      var rect = els.stage.getBoundingClientRect();
      var oldScale = state.scale;
      var nextScale = clampScale(oldScale * factor);
      if (nextScale === oldScale) return;

      var stageX = clientX - rect.left;
      var stageY = clientY - rect.top;
      var graphX = (stageX - state.panX) / oldScale;
      var graphY = (stageY - state.panY) / oldScale;
      state.scale = nextScale;
      state.panX = stageX - graphX * nextScale;
      state.panY = stageY - graphY * nextScale;
      render();
    }

    function zoomFromCenter(factor) {
      var rect = els.stage.getBoundingClientRect();
      zoomAt(rect.left + rect.width / 2, rect.top + rect.height / 2, factor);
    }

    function render() {
      var list = visibleNodes();
      computeLayout(list);
      els.nodes.innerHTML = '';
      els.edges.innerHTML = '';
      els.empty.hidden = state.nodes.length !== 0;

      list.forEach(function(node) {
        var pos = state.positions[node.id];
        var card = document.createElement('article');
        card.className = 'node-card' + (state.selected === node.id ? ' selected' : '');
        card.style.left = pos.x + 'px';
        card.style.top = pos.y + 'px';
        card.dataset.id = node.id;
        var count = childCount(node.id);
        var expandedText = count ? (state.expanded[node.id] ? 'open ' : 'closed ') + count : 'leaf';
        var snippet = String(node.content || node.url || node.id || '').trim().slice(0, 180);
        card.innerHTML =
          '<div class="node-head">' +
            '<span class="dot ' + nodeKind(node) + '"></span>' +
            '<span class="node-title" title="' + escapeHTML(nodeName(node)) + '">' + escapeHTML(nodeName(node)) + '</span>' +
          '</div>' +
          '<div class="node-type">' +
            '<span>' + escapeHTML(node.type || 'node') + '</span>' +
            '<span>embedded ' + escapeHTML(node.embedding_length || 0) + '</span>' +
            '<span class="expand-pill">' + escapeHTML(expandedText) + '</span>' +
          '</div>' +
          (snippet ? '<div class="node-snippet">' + escapeHTML(snippet) + '</div>' : '');
        card.addEventListener('click', function(event) {
          event.stopPropagation();
          toggleNode(node.id);
        });
        els.nodes.appendChild(card);
      });

      drawEdges(list);
      applyTransform();
      updateStats();
    }

    function drawEdges(list) {
      var visibleSet = {};
      list.forEach(function(node) { visibleSet[node.id] = true; });
      state.edges.forEach(function(edge) {
        if (!visibleSet[edge.source_id] || !visibleSet[edge.target_id]) return;
        var source = state.positions[edge.source_id];
        var target = state.positions[edge.target_id];
        if (!source || !target) return;
        var x1 = state.panX + (source.x + source.w) * state.scale;
        var y1 = state.panY + (source.y + 38) * state.scale;
        var x2 = state.panX + target.x * state.scale;
        var y2 = state.panY + (target.y + 38) * state.scale;
        var mid = Math.max(28, (x2 - x1) / 2);
        var path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
        path.setAttribute('d', 'M ' + x1 + ' ' + y1 + ' C ' + (x1 + mid) + ' ' + y1 + ', ' + (x2 - mid) + ' ' + y2 + ', ' + x2 + ' ' + y2);
        path.setAttribute('fill', 'none');
        path.setAttribute('stroke', edge.type === 'DECLARES' ? '#7fa8c9' : edge.type === 'LINKS_TO' ? '#aab1ad' : '#8ec19f');
        path.setAttribute('stroke-width', edge.type === 'HAS_SECTION' ? '1.4' : '1.8');
        path.setAttribute('stroke-linecap', 'round');
        els.edges.appendChild(path);

        var label = document.createElementNS('http://www.w3.org/2000/svg', 'text');
        label.setAttribute('x', (x1 + x2) / 2);
        label.setAttribute('y', (y1 + y2) / 2 - 4);
        label.setAttribute('text-anchor', 'middle');
        label.setAttribute('class', 'edge-label');
        label.textContent = edge.type;
        els.edges.appendChild(label);
      });
    }

    function showProperties(id, skipFetch) {
      var node = state.details[id] || state.byId[id];
      if (!node) return;
      state.selected = id;
      els.properties.classList.add('visible');
      els.panelTitle.textContent = nodeName(node);
      els.panelDot.className = 'dot ' + nodeKind(node);

      var outgoing = (state.children[id] || []).map(function(childID) {
        return { dir: 'child', type: edgeTypeBetween(id, childID), node: state.byId[childID] };
      }).filter(function(item) { return item.node; });
      var incoming = (state.parents[id] || []).map(function(parentID) {
        return { dir: 'parent', type: edgeTypeBetween(parentID, id), node: state.byId[parentID] };
      }).filter(function(item) { return item.node; });

      var content = node.content || '';
      var relationsHTML = incoming.concat(outgoing).slice(0, 80).map(function(item) {
        return '<div class="relation" data-id="' + escapeHTML(item.node.id) + '">' +
          '<span>' + escapeHTML(item.dir) + '<br>' + escapeHTML(item.type) + '</span>' +
          '<div><strong>' + escapeHTML(nodeName(item.node)) + '</strong><small>' + escapeHTML(item.node.type || '') + ' · ' + escapeHTML(shortID(item.node.id)) + '</small></div>' +
        '</div>';
      }).join('');
      if (!relationsHTML) relationsHTML = '<div class="property wide"><label>Relations</label><div>None</div></div>';

      els.propertiesBody.innerHTML =
        '<div class="property-grid">' +
          '<div class="property"><label>Domain</label><div>' + escapeHTML(node.domain || '') + '</div></div>' +
          '<div class="property"><label>Type</label><div>' + escapeHTML(node.type || '') + '</div></div>' +
          '<div class="property"><label>Embedding</label><div>' + escapeHTML(node.embedding_length || 0) + ' floats</div></div>' +
          '<div class="property"><label>Edges</label><div>' + incoming.length + ' in · ' + outgoing.length + ' out</div></div>' +
          '<div class="property wide"><label>ID</label><div>' + escapeHTML(node.id || '') + '</div></div>' +
          (node.path ? '<div class="property wide"><label>Codebase path</label><div>' + escapeHTML(node.path) + '</div></div>' : '') +
          (node.url ? '<div class="property wide"><label>URL</label><div>' + escapeHTML(node.url) + '</div></div>' : '') +
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
          showProperties(id);
        });
      });
      els.propertiesBody.querySelectorAll('.relation').forEach(function(rel) {
        rel.addEventListener('click', function() {
          var nextID = rel.dataset.id;
          state.visible[nextID] = true;
          state.selected = nextID;
          render();
          showProperties(nextID);
        });
      });
      document.getElementById('expand-selected').addEventListener('click', function() {
        expand(id);
        render();
        showProperties(id);
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
            render();
          }
        }).catch(function() {});
      }
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
      ingestGraph(await res.json());
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
    }

    function focusMatches(matches) {
      state.visible = {};
      state.expanded = {};
      matches.forEach(function(node) {
        state.visible[node.id] = true;
        (state.parents[node.id] || []).forEach(function(parentID) {
          state.visible[parentID] = true;
          state.expanded[parentID] = true;
        });
        (state.children[node.id] || []).forEach(function(childID) {
          state.visible[childID] = true;
        });
      });
      if (matches[0]) {
        state.selected = matches[0].id;
        showProperties(matches[0].id);
      }
      render();
    }

    function fitToView() {
      state.scale = 1;
      state.panX = 16;
      state.panY = 18;
      render();
    }

    function startStagePan(event) {
      if (event.target.closest && event.target.closest('.node-card')) return;
      state.isPanning = true;
      state.panMoved = false;
      state.panStartX = event.clientX;
      state.panStartY = event.clientY;
      state.panOriginX = state.panX;
      state.panOriginY = state.panY;
      els.stage.classList.add('panning');
      event.preventDefault();
    }

    function moveStagePan(event) {
      if (!state.isPanning) return;
      var dx = event.clientX - state.panStartX;
      var dy = event.clientY - state.panStartY;
      if (Math.abs(dx) + Math.abs(dy) > 3) state.panMoved = true;
      state.panX = state.panOriginX + dx;
      state.panY = state.panOriginY + dy;
      render();
    }

    function stopStagePan() {
      if (!state.isPanning) return;
      state.isPanning = false;
      els.stage.classList.remove('panning');
    }

    function toast(message) {
      els.toast.textContent = message;
      els.toast.classList.add('visible');
      window.clearTimeout(toast.timer);
      toast.timer = window.setTimeout(function() {
        els.toast.classList.remove('visible');
      }, 2600);
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
    els.fit.addEventListener('click', fitToView);
    els.zoomOut.addEventListener('click', function() { zoomFromCenter(0.82); });
    els.zoomIn.addEventListener('click', function() { zoomFromCenter(1.22); });
    els.panelClose.addEventListener('click', function() { els.properties.classList.remove('visible'); });
    els.filter.addEventListener('input', function() {
      state.filter = els.filter.value.trim().toLowerCase();
      render();
    });
    els.stage.addEventListener('wheel', function(event) {
      event.preventDefault();
      zoomAt(event.clientX, event.clientY, event.deltaY > 0 ? 0.88 : 1.14);
    }, { passive: false });
    els.stage.addEventListener('mousedown', startStagePan);
    document.addEventListener('mousemove', moveStagePan);
    document.addEventListener('mouseup', stopStagePan);
    els.stage.addEventListener('click', function() {
      if (state.panMoved) {
        state.panMoved = false;
        return;
      }
      state.selected = '';
      render();
    });
    els.agentSearch.addEventListener('click', runAgentSearch);
    els.agentNeighbors.addEventListener('click', runAgentNeighbors);
    els.agentQuery.addEventListener('keydown', function(event) {
      if (event.key === 'Enter') runAgentSearch();
    });
    window.addEventListener('resize', render);
    makeDraggable(els.properties, els.propertiesHead);
    reloadGraph();
  </script>
</body>
</html>
`
