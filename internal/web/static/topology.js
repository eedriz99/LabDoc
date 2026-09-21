// Network topology editor. Vanilla JS over an SVG canvas; no build step.
// Node/link shapes and sizes mirror renderTopologySVG in topology.go, which
// produces the exported/shared image. Keep the two in step.
(function () {
  "use strict";
  var root = document.getElementById("topo");
  if (!root) return;

  var NS = "http://www.w3.org/2000/svg";
  var W = 2000, H = 1200, NODE_H = 56, GRID = 10, MAX_LABEL = 80;
  var topoId = root.dataset.id;

  function $(id) { return document.getElementById(id); }
  function json(id) { return JSON.parse($(id).textContent); }

  var kinds = json("kinds-json");
  var inventory = json("inventory-json");
  var doc = json("layout-json");
  var nodes = doc.nodes || [];
  var links = doc.links || [];
  var kindMap = {};
  kinds.forEach(function (k) { kindMap[k.key] = k; });

  var svg = $("canvas");
  var nameInput = $("t-name");
  var statusEl = $("status");

  var selected = null;   // {type: "node"|"link", id}
  var linkMode = false;
  var linkFrom = null;   // node id awaiting a target
  var drag = null;
  window.__topoDirty = false;

  // ---- helpers ------------------------------------------------------------
  function el(name, attrs, text) {
    var e = document.createElementNS(NS, name);
    for (var k in attrs) e.setAttribute(k, attrs[k]);
    if (text != null) e.textContent = text;
    return e;
  }
  function nodeW(n) { return Math.max(140, n.label.length * 8 + 32); }
  function snap(v) { return Math.round(v / GRID) * GRID; }
  function clamp(v, lo, hi) { return Math.max(lo, Math.min(hi, v)); }
  function byId(list, id) {
    for (var i = 0; i < list.length; i++) if (list[i].id === id) return list[i];
    return null;
  }
  function idInUse(id) { return !!(byId(nodes, id) || byId(links, id)); }
  var seq = nodes.length + links.length;
  function newId(prefix) {
    var id;
    do { seq++; id = prefix + seq; } while (idInUse(id));
    return id;
  }
  function slugify(s) {
    s = s.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
    return s.slice(0, 60).replace(/-+$/, "") || "topology";
  }
  function setStatus(msg, bad) {
    statusEl.textContent = msg || "";
    statusEl.style.color = bad ? "var(--pico-del-color, #c62828)" : "";
  }
  function setDirty(d) {
    window.__topoDirty = d;
    if (d) setStatus("Unsaved changes");
  }

  // ---- rendering ----------------------------------------------------------
  function render() {
    while (svg.firstChild) svg.removeChild(svg.firstChild);
    svg.setAttribute("class", linkMode ? "linking" : "");
    svg.setAttribute("font-family", "system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif");

    var defs = el("defs", {});
    var pat = el("pattern", { id: "grid", width: 20, height: 20, patternUnits: "userSpaceOnUse" });
    pat.appendChild(el("path", { d: "M20 0H0V20", fill: "none", stroke: "#eef2f7", "stroke-width": 1 }));
    defs.appendChild(pat);
    svg.appendChild(defs);
    svg.appendChild(el("rect", { x: 0, y: 0, width: W, height: H, fill: "url(#grid)", "data-bg": 1 }));

    links.forEach(function (l) {
      var a = byId(nodes, l.from), b = byId(nodes, l.to);
      if (!a || !b) return;
      var sel = selected && selected.type === "link" && selected.id === l.id;
      var x1 = a.x + nodeW(a) / 2, y1 = a.y + NODE_H / 2;
      var x2 = b.x + nodeW(b) / 2, y2 = b.y + NODE_H / 2;
      var g = el("g", { "data-link": l.id, "class": "link" });
      g.appendChild(el("line", { x1: x1, y1: y1, x2: x2, y2: y2, stroke: sel ? "#2563eb" : "#64748b", "stroke-width": sel ? 4 : 2 }));
      g.appendChild(el("line", { x1: x1, y1: y1, x2: x2, y2: y2, stroke: "transparent", "stroke-width": 14 }));
      if (l.label) {
        var mx = (x1 + x2) / 2, my = (y1 + y2) / 2, w = l.label.length * 7 + 12;
        g.appendChild(el("rect", { x: mx - w / 2, y: my - 10, width: w, height: 20, rx: 4, fill: "#fff", stroke: "#cbd5e1" }));
        g.appendChild(el("text", { x: mx, y: my + 4, "font-size": 12, "text-anchor": "middle", fill: "#334155" }, l.label));
      }
      svg.appendChild(g);
    });

    nodes.forEach(function (n) {
      var k = kindMap[n.kind] || kindMap.other;
      var sel = selected && selected.type === "node" && selected.id === n.id;
      var w = nodeW(n);
      var g = el("g", { "data-node": n.id, "class": "node" });
      g.appendChild(el("rect", {
        x: n.x, y: n.y, width: w, height: NODE_H, rx: 8, fill: "#fff", stroke: k.color,
        "stroke-width": sel ? 4 : 2, "stroke-dasharray": linkFrom === n.id ? "6 4" : "none",
      }));
      g.appendChild(el("text", { x: n.x + w / 2, y: n.y + 20, "font-size": 10, "font-weight": 600, "text-anchor": "middle", fill: k.color }, k.label.toUpperCase()));
      g.appendChild(el("text", { x: n.x + w / 2, y: n.y + 40, "font-size": 14, "font-weight": 700, "text-anchor": "middle", fill: "#0f172a" }, n.label));
      svg.appendChild(g);
    });
  }

  function syncProps() {
    var item = null;
    if (selected) item = selected.type === "node" ? byId(nodes, selected.id) : byId(links, selected.id);
    $("props-none").hidden = !!item;
    $("prop-label-wrap").hidden = !item;
    $("prop-kind-wrap").hidden = !(item && selected.type === "node");
    if (item) {
      $("prop-label").value = item.label;
      if (selected.type === "node") $("prop-kind").value = item.kind;
    }
  }

  function select(sel) {
    selected = sel;
    syncProps();
    render();
  }

  // ---- interaction --------------------------------------------------------
  function point(ev) {
    var r = svg.getBoundingClientRect();
    return { x: ev.clientX - r.left, y: ev.clientY - r.top };
  }

  function addLink(fromId, toId) {
    var dup = links.some(function (l) {
      return (l.from === fromId && l.to === toId) || (l.from === toId && l.to === fromId);
    });
    if (dup) { setStatus("Those nodes are already linked."); return null; }
    var l = { id: newId("l"), from: fromId, to: toId, label: "" };
    links.push(l);
    setDirty(true);
    return l;
  }

  svg.addEventListener("pointerdown", function (ev) {
    var nodeEl = ev.target.closest("[data-node]");
    var linkEl = ev.target.closest("[data-link]");
    if (nodeEl) {
      var n = byId(nodes, nodeEl.getAttribute("data-node"));
      if (!n) return;
      if (linkMode) {
        if (!linkFrom) {
          linkFrom = n.id;
          setStatus("Now click the node to link to.");
          select({ type: "node", id: n.id });
        } else if (linkFrom === n.id) {
          linkFrom = null;
          setStatus("");
          render();
        } else {
          var l = addLink(linkFrom, n.id);
          linkFrom = null;
          if (l) select({ type: "link", id: l.id }); else render();
        }
        return;
      }
      select({ type: "node", id: n.id });
      var p = point(ev);
      drag = { id: n.id, dx: p.x - n.x, dy: p.y - n.y };
      svg.setPointerCapture(ev.pointerId);
      ev.preventDefault();
    } else if (linkEl) {
      select({ type: "link", id: linkEl.getAttribute("data-link") });
    } else {
      linkFrom = null;
      select(null);
    }
  });

  svg.addEventListener("pointermove", function (ev) {
    if (!drag) return;
    var n = byId(nodes, drag.id);
    if (!n) return;
    var p = point(ev);
    var x = clamp(snap(p.x - drag.dx), 0, W - nodeW(n));
    var y = clamp(snap(p.y - drag.dy), 0, H - NODE_H);
    if (x !== n.x || y !== n.y) {
      n.x = x; n.y = y;
      setDirty(true);
      render();
    }
  });

  function endDrag(ev) {
    if (!drag) return;
    drag = null;
    if (svg.hasPointerCapture(ev.pointerId)) svg.releasePointerCapture(ev.pointerId);
  }
  svg.addEventListener("pointerup", endDrag);
  svg.addEventListener("pointercancel", endDrag);

  function deleteSelected() {
    if (!selected) return;
    if (selected.type === "node") {
      var id = selected.id;
      nodes = nodes.filter(function (n) { return n.id !== id; });
      links = links.filter(function (l) { return l.from !== id && l.to !== id; });
    } else {
      var lid = selected.id;
      links = links.filter(function (l) { return l.id !== lid; });
    }
    selected = null;
    linkFrom = null;
    setDirty(true);
    syncProps();
    render();
  }

  document.addEventListener("keydown", function (ev) {
    if (ev.key !== "Delete" && ev.key !== "Backspace") return;
    var t = document.activeElement && document.activeElement.tagName;
    if (t === "INPUT" || t === "SELECT" || t === "TEXTAREA") return;
    if (!$("canvas")) return;
    ev.preventDefault();
    deleteSelected();
  });

  // ---- nodes: add / edit --------------------------------------------------
  // Finds a free spot in a row, wrapping to the next row near the right edge.
  function placeInRow(y, w) {
    for (;;) {
      var x = 40;
      nodes.forEach(function (n) {
        if (Math.abs(n.y - y) < 1) x = Math.max(x, n.x + nodeW(n) + 40);
      });
      if (x + w <= W - 40 || y + NODE_H + 40 > H - NODE_H) return { x: Math.min(x, W - w), y: y };
      y += 100;
    }
  }

  function addNode(kind, label, ref, y) {
    var n = { id: newId("n"), kind: kind, label: label.slice(0, MAX_LABEL), x: 0, y: 0 };
    if (ref) n.ref = ref;
    var pos = placeInRow(y, nodeW(n));
    n.x = pos.x; n.y = pos.y;
    nodes.push(n);
    setDirty(true);
    return n;
  }

  function hasRef(type, id) {
    return nodes.some(function (n) { return n.ref && n.ref.type === type && n.ref.id === id; });
  }
  function findRef(type, id) {
    for (var i = 0; i < nodes.length; i++) {
      var r = nodes[i].ref;
      if (r && r.type === type && r.id === id) return nodes[i];
    }
    return null;
  }

  $("btn-add").addEventListener("click", function () {
    var kind = $("new-kind").value;
    var n = addNode(kind, kindMap[kind].label, null, 60 + (nodes.length % 8) * 100);
    select({ type: "node", id: n.id });
    $("prop-label").focus();
    $("prop-label").select();
  });

  $("btn-del").addEventListener("click", deleteSelected);

  $("btn-link").addEventListener("click", function () {
    linkMode = !linkMode;
    linkFrom = null;
    this.textContent = "Link mode: " + (linkMode ? "on" : "off");
    this.setAttribute("aria-pressed", String(linkMode));
    setStatus(linkMode ? "Click a node, then the node to link it to." : "");
    render();
  });

  $("prop-label").addEventListener("input", function () {
    if (!selected) return;
    var item = selected.type === "node" ? byId(nodes, selected.id) : byId(links, selected.id);
    if (!item) return;
    item.label = this.value.slice(0, MAX_LABEL);
    setDirty(true);
    render();
  });

  $("prop-kind").addEventListener("change", function () {
    if (!selected || selected.type !== "node") return;
    var n = byId(nodes, selected.id);
    if (!n) return;
    n.kind = this.value;
    setDirty(true);
    render();
  });

  nameInput.addEventListener("input", function () { setDirty(true); });

  // ---- inventory ----------------------------------------------------------
  var INV_ROWS = { vlans: 60, devices: 220, services: 380 };
  var INV_KIND = { vlans: "vlan", devices: "server", services: "service" };

  (function fillInventory() {
    var sel = $("inv-select");
    function group(label, type, items) {
      if (!items.length) return;
      var og = document.createElement("optgroup");
      og.label = label;
      items.forEach(function (it) {
        var o = document.createElement("option");
        o.value = type + ":" + it.id;
        o.textContent = it.label;
        og.appendChild(o);
      });
      sel.appendChild(og);
    }
    group("VLANs", "vlans", inventory.vlans);
    group("Devices", "devices", inventory.devices);
    group("Services", "services", inventory.services);
    if (!sel.options.length) {
      var o = document.createElement("option");
      o.textContent = "No inventory yet";
      sel.appendChild(o);
      sel.disabled = true;
      $("btn-inv-add").disabled = true;
      $("btn-inv-all").disabled = true;
    }
  })();

  function addInventoryNode(type, item) {
    if (hasRef(type, item.id)) return null;
    return addNode(INV_KIND[type], item.label, { type: type, id: item.id }, INV_ROWS[type]);
  }

  // Links devices to their VLANs and services to their host (or VLAN), for
  // whichever of those nodes are on the diagram. Skips links that exist.
  function linkInventory() {
    function connect(a, b) {
      if (a && b) {
        var exists = links.some(function (l) {
          return (l.from === a.id && l.to === b.id) || (l.from === b.id && l.to === a.id);
        });
        if (!exists) addLink(a.id, b.id);
      }
    }
    inventory.devices.forEach(function (d) {
      var dn = findRef("devices", d.id);
      d.vlans.forEach(function (v) { connect(dn, findRef("vlans", v)); });
    });
    inventory.services.forEach(function (s) {
      var sn = findRef("services", s.id);
      if (s.host && findRef("devices", s.host)) connect(sn, findRef("devices", s.host));
      else if (s.vlan) connect(sn, findRef("vlans", s.vlan));
    });
  }

  $("btn-inv-add").addEventListener("click", function () {
    var parts = $("inv-select").value.split(":");
    var type = parts[0], id = Number(parts[1]);
    var item = (inventory[type] || []).filter(function (i) { return i.id === id; })[0];
    if (!item) return;
    var n = addInventoryNode(type, item);
    if (!n) { setStatus("Already on the diagram."); return; }
    linkInventory();
    select({ type: "node", id: n.id });
  });

  $("btn-inv-all").addEventListener("click", function () {
    var added = 0;
    ["vlans", "devices", "services"].forEach(function (type) {
      inventory[type].forEach(function (it) { if (addInventoryNode(type, it)) added++; });
    });
    linkInventory();
    setStatus(added ? "Imported " + added + " item(s). Drag to arrange, then save." : "Everything is already on the diagram.");
    render();
  });

  // ---- save / export / share ---------------------------------------------
  function save() {
    setStatus("Saving…");
    return fetch("/topologies/" + topoId + "/save", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: nameInput.value, nodes: nodes, links: links }),
    }).then(function (r) {
      if (!r.ok) {
        return r.text().then(function (t) { throw new Error((t || "HTTP " + r.status).trim()); });
      }
      window.__topoDirty = false;
      setStatus("Saved");
    }).catch(function (e) {
      setStatus("Not saved: " + e.message, true);
      throw e;
    });
  }

  $("btn-save").addEventListener("click", function () { save().catch(function () {}); });

  // Exports render the saved diagram on the server, so save first.
  $("btn-svg").addEventListener("click", function () {
    save().then(function () {
      window.location.href = "/topologies/" + topoId + "/image.svg?download=1";
    }).catch(function () {});
  });

  $("btn-png").addEventListener("click", function () {
    save().then(function () {
      setStatus("Rendering PNG…");
      return window.LabDocExport.png("/topologies/" + topoId + "/image.svg", slugify(nameInput.value) + ".png");
    }).then(function () { setStatus("Saved; PNG downloaded"); })
      .catch(function (e) { if (e) setStatus("Could not export: " + e.message, true); });
  });

  var shareToken = root.dataset.share || "";
  function showShare() {
    var on = !!shareToken;
    $("share-box").hidden = !on;
    $("share-note").hidden = !on;
    if (on) $("share-url").value = window.location.origin + "/share/" + shareToken;
  }
  showShare();

  $("btn-share").addEventListener("click", function () {
    fetch("/topologies/" + topoId + "/share", { method: "POST" })
      .then(function (r) { if (!r.ok) throw new Error("HTTP " + r.status); return r.json(); })
      .then(function (d) { shareToken = d.token; showShare(); $("share-url").select(); })
      .catch(function (e) { setStatus("Could not share: " + e.message, true); });
  });

  $("btn-unshare").addEventListener("click", function () {
    fetch("/topologies/" + topoId + "/unshare", { method: "POST" })
      .then(function (r) { if (!r.ok) throw new Error("HTTP " + r.status); })
      .then(function () { shareToken = ""; showShare(); setStatus("Sharing stopped; the old link no longer works."); })
      .catch(function (e) { setStatus("Could not stop sharing: " + e.message, true); });
  });

  $("btn-copy").addEventListener("click", function () {
    var input = $("share-url");
    input.select();
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(input.value).then(
        function () { setStatus("Link copied"); },
        function () { setStatus("Press Ctrl+C to copy the selected link."); }
      );
    } else {
      setStatus("Press Ctrl+C to copy the selected link.");
    }
  });

  // ---- leaving with unsaved work -----------------------------------------
  window.addEventListener("beforeunload", function (ev) {
    if (window.__topoDirty) { ev.preventDefault(); ev.returnValue = ""; }
  });
  // In-app navigation is boosted by htmx, which never fires beforeunload.
  document.addEventListener("htmx:beforeRequest", function (ev) {
    if (!window.__topoDirty) return;
    if (!window.confirm("You have unsaved changes. Leave without saving?")) ev.preventDefault();
    else window.__topoDirty = false;
  });

  syncProps();
  render();
})();
