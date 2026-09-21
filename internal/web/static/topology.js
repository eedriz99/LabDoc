// Network topology editor. Vanilla JS over an SVG canvas; no build step.
// Node/link shapes and sizes mirror renderTopologySVG in topology.go, which
// produces the exported/shared image. Keep the two in step.
(function () {
  "use strict";
  var root = document.getElementById("topo");
  if (!root) return;

  var NS = "http://www.w3.org/2000/svg";
  var W = 2000, H = 1200, NODE_H = 56, GRID = 10, MAX_LABEL = 80;
  // Same values as topoLinkStyle in topology.go.
  var LINK_STYLES = {
    "": { color: "#64748b", width: 2 },
    access: { color: "#15803d", width: 2 },
    trunk: { color: "#7c3aed", width: 4 },
  };
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

    var labels = [];
    links.forEach(function (l) {
      var a = byId(nodes, l.from), b = byId(nodes, l.to);
      if (!a || !b) return;
      var sel = selected && selected.type === "link" && selected.id === l.id;
      var x1 = a.x + nodeW(a) / 2, y1 = a.y + NODE_H / 2;
      var x2 = b.x + nodeW(b) / 2, y2 = b.y + NODE_H / 2;
      var g = el("g", { "data-link": l.id, "class": "link" });
      var ls = LINK_STYLES[l.style] || LINK_STYLES[""];
      g.appendChild(el("line", { x1: x1, y1: y1, x2: x2, y2: y2, stroke: sel ? "#2563eb" : ls.color, "stroke-width": sel ? ls.width + 2 : ls.width }));
      g.appendChild(el("line", { x1: x1, y1: y1, x2: x2, y2: y2, stroke: "transparent", "stroke-width": 14 }));
      if (l.label) labels.push({ id: l.id, text: l.label, x: (x1 + x2) / 2, y: (y1 + y2) / 2 });
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

    // Link labels last so nodes never cover them (clicking one selects the link).
    labels.forEach(function (lb) {
      var w = lb.text.length * 7 + 12;
      var g = el("g", { "data-link": lb.id, "class": "link" });
      g.appendChild(el("rect", { x: lb.x - w / 2, y: lb.y - 10, width: w, height: 20, rx: 4, fill: "#fff", stroke: "#cbd5e1" }));
      g.appendChild(el("text", { x: lb.x, y: lb.y + 4, "font-size": 12, "text-anchor": "middle", fill: "#334155" }, lb.text));
      svg.appendChild(g);
    });
    updateSyncNote();
  }

  function syncProps() {
    var item = null;
    if (selected) item = selected.type === "node" ? byId(nodes, selected.id) : byId(links, selected.id);
    $("props-none").hidden = !!item;
    $("prop-label-wrap").hidden = !item;
    $("prop-kind-wrap").hidden = !(item && selected.type === "node");
    $("prop-style-wrap").hidden = !(item && selected.type === "link");
    var isNode = !!item && selected.type === "node";
    var isLink = !!item && selected.type === "link";
    // Only device-like nodes can be tied to an inventory device (VLAN/service nodes keep their own refs).
    var refOK = isNode && (!item.ref || item.ref.type === "devices");
    $("prop-ref-wrap").hidden = !refOK;
    $("prop-conn").hidden = !isLink;
    if (item) {
      $("prop-label").value = item.label;
      if (isNode) $("prop-kind").value = item.kind;
      else $("prop-style").value = item.style || "";
      if (refOK) $("prop-ref").value = item.ref ? String(item.ref.id) : "";
      if (isLink) syncConnPanel(item);
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
        if (Math.abs(n.y - y) < 1) x = Math.max(x, n.x + nodeW(n) + 100);
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
    $("link-state").textContent = linkMode ? "on" : "off";
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

  $("prop-style").addEventListener("change", function () {
    if (!selected || selected.type !== "link") return;
    var l = byId(links, selected.id);
    if (!l) return;
    if (this.value) l.style = this.value; else delete l.style;
    setDirty(true);
    syncProps(); // the "Record as connection" link carries the mode
    render();
  });

  nameInput.addEventListener("input", function () { setDirty(true); });

  // ---- inventory ----------------------------------------------------------
  // Rows: VLANs on top, then network gear (so links to switches/routers run
  // between rows instead of behind other nodes), then compute devices, then services.
  var INV_ROWS = { vlans: 60, network: 220, devices: 380, services: 540 };
  var NETWORK_KINDS = { router: 1, firewall: 1, switch: 1, ap: 1 };
  function invRow(type, item) {
    if (type === "devices" && NETWORK_KINDS[item.kind]) return INV_ROWS.network;
    return INV_ROWS[type];
  }
  var INV_KIND = { vlans: "vlan", devices: "server", services: "service" };

  function fillInventory() {
    var sel = $("inv-select");
    sel.innerHTML = "";
    sel.disabled = false;
    $("btn-inv-add").disabled = false;
    $("btn-inv-all").disabled = false;
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
  }
  fillInventory();

  // ---- linking diagram devices to Connections records ---------------------
  // A diagram link is just a line. To become a Connections record it needs a
  // mode, VLANs and ports, so the editor sends the user to the pre-filled
  // Connections form instead of guessing.
  var NET_RANK = { switch: 3, router: 2, firewall: 2, ap: 1 };

  function deviceRef(n) { return n && n.ref && n.ref.type === "devices" ? n.ref.id : 0; }

  // Which end is the device and which the switch/router, or null when either
  // end is not tied to an inventory device.
  function linkSides(l) {
    var a = byId(nodes, l.from), b = byId(nodes, l.to);
    var da = deviceRef(a), db = deviceRef(b);
    if (!da || !db || da === db) return null;
    var swIsB = (NET_RANK[b.kind] || 0) >= (NET_RANK[a.kind] || 0);
    return swIsB ? { device: da, sw: db } : { device: db, sw: da };
  }

  function isRecorded(s) {
    return inventory.connections.some(function (c) {
      return (c.device === s.device && c.switch === s.sw) || (c.device === s.sw && c.switch === s.device);
    });
  }

  function syncConnPanel(l) {
    var msg = $("prop-conn-msg"), a = $("prop-conn-link");
    var s = linkSides(l);
    a.hidden = true;
    if (!s) {
      msg.textContent = "To record this link as a connection, both ends must be inventory devices: select a node and choose its inventory device.";
      return;
    }
    if (isRecorded(s)) {
      msg.textContent = "Recorded in Connections.";
      a.href = "/connections";
      a.textContent = "View connections";
      a.hidden = false;
      return;
    }
    var q = "device_id=" + s.device + "&switch_id=" + s.sw;
    if (l.style === "trunk") q += "&mode=Trunk"; else if (l.style === "access") q += "&mode=Access";
    msg.textContent = "Not recorded in Connections yet.";
    a.href = "/connections/new?" + q;
    a.textContent = "Record as connection";
    a.hidden = false;
  }

  function updateSyncNote() {
    var n = links.filter(function (l) { var s = linkSides(l); return s && !isRecorded(s); }).length;
    var note = $("sync-note");
    note.hidden = !n;
    if (n) {
      note.firstElementChild.textContent = n + (n === 1 ? " link between inventory devices is" : " links between inventory devices are") +
        " not recorded in Connections yet. Click a link, then “Record as connection”.";
    }
  }

  function fillDeviceSelect() {
    var sel = $("prop-ref");
    sel.innerHTML = "";
    var none = document.createElement("option");
    none.value = "";
    none.textContent = "— none —";
    sel.appendChild(none);
    inventory.devices.forEach(function (d) {
      var o = document.createElement("option");
      o.value = String(d.id);
      o.textContent = d.label;
      sel.appendChild(o);
    });
  }

  $("prop-ref").addEventListener("change", function () {
    if (!selected || selected.type !== "node") return;
    var n = byId(nodes, selected.id);
    if (!n) return;
    if (this.value) n.ref = { type: "devices", id: Number(this.value) }; else delete n.ref;
    setDirty(true);
    syncProps();
    render();
  });

  // Pick up inventory changes (e.g. a connection recorded in another tab).
  function refreshInventory() {
    fetch("/inventory.json", { cache: "no-store" })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (inv) {
        if (!inv) return;
        inventory = inv;
        fillInventory();
        fillDeviceSelect();
        syncProps();
        render();
      })
      .catch(function () {});
  }
  window.addEventListener("focus", refreshInventory);

  function addInventoryNode(type, item) {
    if (hasRef(type, item.id)) return null;
    // Devices carry their own node type (switch, router, firewall, ...).
    return addNode(item.kind || INV_KIND[type], item.label, { type: type, id: item.id }, invRow(type, item));
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

    // Device-to-switch connections become one link per device/switch pair,
    // labeled "Trunk: 10,30 (native 1)" or "Access: 10" and colored by mode.
    // Several cables between the same pair share a link with combined labels.
    var pairs = {};
    inventory.connections.forEach(function (c) {
      var k = c.device + ":" + c.switch;
      (pairs[k] = pairs[k] || []).push(c);
    });
    Object.keys(pairs).forEach(function (k) {
      var cs = pairs[k];
      var a = findRef("devices", cs[0].device), b = findRef("devices", cs[0].switch);
      if (!a || !b) return;
      var label = cs.map(connLabel).join(" / ").slice(0, MAX_LABEL);
      var style = cs.some(function (c) { return c.mode === "Trunk"; }) ? "trunk" : "access";
      var existing = links.filter(function (l) {
        return (l.from === a.id && l.to === b.id) || (l.from === b.id && l.to === a.id);
      })[0];
      if (existing) {
        // Never overwrite a label the user already set.
        if (!existing.label) { existing.label = label; existing.style = style; setDirty(true); }
      } else {
        var l = addLink(a.id, b.id);
        if (l) { l.label = label; l.style = style; }
      }
    });
  }

  function vlanNum(id) {
    var v = inventory.vlans.filter(function (x) { return x.id === id; })[0];
    return v ? v.num : id;
  }
  function connLabel(c) {
    if (c.mode === "Trunk") {
      var s = "Trunk: " + c.tagged.map(vlanNum).join(",");
      return c.untagged ? s + " (native " + vlanNum(c.untagged) + ")" : s;
    }
    return "Access: " + (c.untagged ? vlanNum(c.untagged) : "?");
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

  fillDeviceSelect();
  syncProps();
  render();
})();
