// Network topology editor. Vanilla JS over an SVG canvas; no build step.
// Node/link shapes and sizes mirror renderTopologySVG in topology.go, which
// produces the exported/shared image. Keep the two in step.
(function () {
  "use strict";
  var root = document.getElementById("topo");
  if (!root) return;

  var NS = "http://www.w3.org/2000/svg";
  var W = 2000, H = 1200, NODE_H = 56, DETAIL_H = 16, CHIP_ROW_H = 20, GRID = 10, MAX_LABEL = 80, MAX_NODE_VLANS = 6;
  var cw = W, ch = H; // current canvas size; grows with the diagram
  // Same values as topoLinkTypes / topoLinkStyle in topology.go.
  var LINK_TYPES = [
    { key: "trunk", word: "Trunk", legend: "Trunk cable: carries several VLANs", color: "#7c3aed", width: 4, dash: "" },
    { key: "access", word: "Access", legend: "Access cable: carries one VLAN", color: "#15803d", width: 2, dash: "" },
    { key: "link", word: "Link", legend: "Cable, VLANs not specified", color: "#64748b", width: 2, dash: "" },
    { key: "logical", word: "Logical", legend: "Dashed: a service running on a device, not a cable", color: "#94a3b8", width: 2, dash: "6 5" },
  ];
  // Same as topoVlanPalette: a VLAN's color is its position in the sorted list.
  var VLAN_PALETTE = ["#2563eb", "#be185d", "#15803d", "#b45309", "#6d28d9", "#0e7490", "#b91c1c", "#4d7c0f"];
  var topoId = root.dataset.id;

  function $(id) { return document.getElementById(id); }
  function json(id) { return JSON.parse($(id).textContent); }

  var kinds = json("kinds-json");
  var inventory = json("inventory-json");
  var doc = json("layout-json");
  var nodes = doc.nodes || [];
  var links = doc.links || [];
  var vlans = doc.vlans || [];          // [{num, name}] shown in the legend and offered as tags
  var hideLabels = !!doc.hideLabels;    // false: every link is labeled (its label, else its type)
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
  function chipText(num) { return "VLAN " + num; }
  function chipW(num) { return Math.ceil(chipText(num).length * 6.4) + 14; }
  function chipsW(nums) {
    return nums.reduce(function (w, v, i) { return w + (i ? 4 : 0) + chipW(v); }, 0);
  }
  function nodeW(n) {
    var w = Math.max(140, n.label.length * 8 + 32);
    if (n.detail) w = Math.max(w, n.detail.length * 6.4 + 32);
    if (n.vlans && n.vlans.length) w = Math.max(w, chipsW(n.vlans) + 24);
    return w;
  }
  function nodeH(n) {
    return NODE_H + (n.detail ? DETAIL_H : 0) + (n.vlans && n.vlans.length ? CHIP_ROW_H : 0);
  }
  function sortVlans() { vlans.sort(function (a, b) { return a.num - b.num; }); }
  sortVlans();
  function vlanColor(num) {
    for (var i = 0; i < vlans.length; i++) if (vlans[i].num === num) return VLAN_PALETTE[i % VLAN_PALETTE.length];
    return "#64748b";
  }
  function isLogicalKind(kind) { return !!(kindMap[kind] && kindMap[kind].logical); }
  // A link touching a logical node (a service, or a legacy VLAN box) is logical, never a cable.
  function linkType(l, a, b) {
    if (l.style === "logical" || isLogicalKind(a.kind) || isLogicalKind(b.kind)) return "logical";
    if (l.style === "trunk" || l.style === "access") return l.style;
    return "link";
  }
  function typeInfo(key) {
    for (var i = 0; i < LINK_TYPES.length; i++) if (LINK_TYPES[i].key === key) return LINK_TYPES[i];
    return LINK_TYPES[2];
  }
  function linkText(l, key, a, b) {
    if (hideLabels) return "";
    if (l.label) return l.label;
    if (key === "logical") return ""; // explained by the legend; see topoLinkLabel
    return typeInfo(key).word;
  }
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

    cw = W; ch = H;
    nodes.forEach(function (n) {
      cw = Math.max(cw, n.x + nodeW(n) + 80);
      ch = Math.max(ch, n.y + nodeH(n) + 80);
    });
    svg.setAttribute("width", cw);
    svg.setAttribute("height", ch);
    svg.setAttribute("viewBox", "0 0 " + cw + " " + ch);

    var defs = el("defs", {});
    var pat = el("pattern", { id: "grid", width: 20, height: 20, patternUnits: "userSpaceOnUse" });
    pat.appendChild(el("path", { d: "M20 0H0V20", fill: "none", stroke: "#eef2f7", "stroke-width": 1 }));
    defs.appendChild(pat);
    svg.appendChild(defs);
    svg.appendChild(el("rect", { x: 0, y: 0, width: cw, height: ch, fill: "url(#grid)", "data-bg": 1 }));

    var labels = [];
    links.forEach(function (l) {
      var a = byId(nodes, l.from), b = byId(nodes, l.to);
      if (!a || !b) return;
      var sel = selected && selected.type === "link" && selected.id === l.id;
      var x1 = a.x + nodeW(a) / 2, y1 = a.y + nodeH(a) / 2;
      var x2 = b.x + nodeW(b) / 2, y2 = b.y + nodeH(b) / 2;
      var key = linkType(l, a, b), ls = typeInfo(key);
      var g = el("g", { "data-link": l.id, "class": "link" });
      var line = el("line", { x1: x1, y1: y1, x2: x2, y2: y2, stroke: sel ? "#2563eb" : ls.color, "stroke-width": sel ? ls.width + 2 : ls.width });
      if (ls.dash) line.setAttribute("stroke-dasharray", ls.dash);
      g.appendChild(line);
      g.appendChild(el("line", { x1: x1, y1: y1, x2: x2, y2: y2, stroke: "transparent", "stroke-width": 14 }));
      var text = linkText(l, key, a, b);
      if (text) labels.push({ id: l.id, text: text, x: (x1 + x2) / 2, y: (y1 + y2) / 2 });
      svg.appendChild(g);
    });

    nodes.forEach(function (n) {
      var k = kindMap[n.kind] || kindMap.other;
      var sel = selected && selected.type === "node" && selected.id === n.id;
      var w = nodeW(n), h = nodeH(n);
      var g = el("g", { "data-node": n.id, "class": "node" });
      // Logical nodes (services) always have a dashed border; "linking from" is shown by a thick border instead.
      g.appendChild(el("rect", {
        x: n.x, y: n.y, width: w, height: h, rx: 8, fill: "#fff", stroke: k.color,
        "stroke-width": sel || (k.logical && linkFrom === n.id) ? 4 : 2,
        "stroke-dasharray": k.logical || linkFrom === n.id ? "6 4" : "none",
      }));
      g.appendChild(el("text", { x: n.x + w / 2, y: n.y + 20, "font-size": 10, "font-weight": 600, "text-anchor": "middle", fill: k.color }, k.label.toUpperCase()));
      g.appendChild(el("text", { x: n.x + w / 2, y: n.y + 40, "font-size": 14, "font-weight": 700, "text-anchor": "middle", fill: "#0f172a" }, n.label));
      var base = NODE_H;
      if (n.detail) {
        g.appendChild(el("text", { x: n.x + w / 2, y: n.y + 56, "font-size": 11, "text-anchor": "middle", fill: "#64748b" }, n.detail));
        base += DETAIL_H;
      }
      if (n.vlans && n.vlans.length) {
        var cx = n.x + (w - chipsW(n.vlans)) / 2;
        n.vlans.forEach(function (v) {
          var wv = chipW(v);
          g.appendChild(el("rect", { x: cx, y: n.y + base - 2, width: wv, height: 16, rx: 8, fill: vlanColor(v) }));
          g.appendChild(el("text", { x: cx + wv / 2, y: n.y + base + 9, "font-size": 10, "font-weight": 700, "text-anchor": "middle", fill: "#fff" }, chipText(v)));
          cx += wv + 4;
        });
      }
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
    renderLegend();
    renderVlanManager();
    updateLegacyNote();
    $("labels-state").textContent = hideLabels ? "off" : "on";
  }

  // The key under the canvas: only what this diagram uses, like the exported image.
  function renderLegend() {
    var box = $("legend");
    box.innerHTML = "";
    var used = {}, kindsUsed = {};
    links.forEach(function (l) {
      var a = byId(nodes, l.from), b = byId(nodes, l.to);
      if (a && b) used[linkType(l, a, b)] = true;
    });
    nodes.forEach(function (n) { kindsUsed[n.kind] = true; });
    function section(title) {
      var d = document.createElement("div");
      d.className = "lg-sec";
      var h = document.createElement("span");
      h.className = "tlabel";
      h.textContent = title;
      d.appendChild(h);
      box.appendChild(d);
      return d;
    }
    function row(parent, iconEl, text) {
      var r = document.createElement("div");
      r.className = "lg-row";
      r.appendChild(iconEl);
      var t = document.createElement("span");
      t.textContent = text;
      r.appendChild(t);
      parent.appendChild(r);
    }
    var lt = LINK_TYPES.filter(function (t) { return used[t.key]; });
    if (lt.length) {
      var sec = section("Lines");
      lt.forEach(function (t) {
        var sv = el("svg", { width: 36, height: 12, viewBox: "0 0 36 12", "aria-hidden": "true" });
        var ln = el("line", { x1: 0, y1: 6, x2: 36, y2: 6, stroke: t.color, "stroke-width": t.width });
        if (t.dash) ln.setAttribute("stroke-dasharray", t.dash);
        sv.appendChild(ln);
        row(sec, sv, t.legend);
      });
    }
    if (vlans.length) {
      var vs = section("VLAN tags");
      vlans.forEach(function (v) {
        var chip = document.createElement("span");
        chip.className = "chip";
        chip.style.background = vlanColor(v.num);
        chip.textContent = chipText(v.num);
        row(vs, chip, v.name);
      });
    }
    var ks = kinds.filter(function (k) { return kindsUsed[k.key]; });
    if (ks.length) {
      var ds = section("Device types");
      ks.forEach(function (k) {
        var sw = document.createElement("span");
        sw.className = "swatch";
        sw.style.borderColor = k.color;
        if (k.logical) sw.style.borderStyle = "dashed";
        row(ds, sw, k.label);
      });
    }
    box.hidden = !box.children.length;
  }

  // ---- VLAN tags: the diagram's VLAN list ---------------------------------
  function addVlanDef(num, name) {
    for (var i = 0; i < vlans.length; i++) {
      if (vlans[i].num === num) {
        if (!vlans[i].name && name) { vlans[i].name = name.slice(0, MAX_LABEL); setDirty(true); }
        return false;
      }
    }
    vlans.push({ num: num, name: (name || "").slice(0, MAX_LABEL) });
    sortVlans();
    setDirty(true);
    return true;
  }

  function removeVlanDef(num) {
    vlans = vlans.filter(function (v) { return v.num !== num; });
    nodes.forEach(function (n) {
      if (n.vlans) {
        n.vlans = n.vlans.filter(function (v) { return v !== num; });
        if (!n.vlans.length) delete n.vlans;
      }
    });
    setDirty(true);
    syncProps();
    render();
  }

  function renderVlanManager() {
    var list = $("vlan-list");
    list.innerHTML = "";
    $("vlan-count").textContent = vlans.length ? " (" + vlans.length + ")" : "";
    vlans.forEach(function (v) {
      var r = document.createElement("div");
      r.className = "lg-row";
      var chip = document.createElement("span");
      chip.className = "chip";
      chip.style.background = vlanColor(v.num);
      chip.textContent = chipText(v.num);
      var t = document.createElement("span");
      t.textContent = v.name || "(no name)";
      var x = document.createElement("button");
      x.type = "button";
      x.className = "icon-btn danger";
      x.title = "Remove " + chipText(v.num) + " from this diagram";
      x.setAttribute("aria-label", x.title);
      x.textContent = "×";
      x.addEventListener("click", function () { removeVlanDef(v.num); });
      r.appendChild(chip); r.appendChild(t); r.appendChild(x);
      list.appendChild(r);
    });
  }

  $("btn-vlan-add").addEventListener("click", function () {
    var num = Number($("vlan-num").value);
    if (!Number.isInteger(num) || num < 1 || num > 4094) { setStatus("VLAN number must be between 1 and 4094.", true); return; }
    if (!addVlanDef(num, $("vlan-name").value.trim())) { setStatus("VLAN " + num + " is already listed."); return; }
    $("vlan-num").value = "";
    $("vlan-name").value = "";
    setStatus("Added " + chipText(num) + ". Select a device to tag it.");
    syncProps();
    render();
  });

  function syncProps() {
    var item = null;
    if (selected) item = selected.type === "node" ? byId(nodes, selected.id) : byId(links, selected.id);
    $("props-none").hidden = !!item;
    $("prop-label-wrap").hidden = !item;
    $("prop-kind-wrap").hidden = !(item && selected.type === "node");
    $("prop-style-wrap").hidden = !(item && selected.type === "link");
    var isNode = !!item && selected.type === "node";
    var isLink = !!item && selected.type === "link";
    $("prop-detail-wrap").hidden = !isNode;
    $("prop-vlans-wrap").hidden = !isNode;
    // Only device-like nodes can be tied to an inventory device (VLAN/service nodes keep their own refs).
    var refOK = isNode && (!item.ref || item.ref.type === "devices");
    $("prop-ref-wrap").hidden = !refOK;
    $("prop-conn").hidden = !isLink;
    if (item) {
      $("prop-label").value = item.label;
      if (isNode) {
        $("prop-kind").value = item.kind;
        $("prop-detail").value = item.detail || "";
        fillVlanChecks(item);
      } else {
        $("prop-style").value = item.style || "";
      }
      if (refOK) $("prop-ref").value = item.ref ? String(item.ref.id) : "";
      if (isLink) syncConnPanel(item);
    }
  }

  // One checkbox per VLAN in the diagram's list; ticked ones become tags on the node.
  function fillVlanChecks(n) {
    var box = $("prop-vlans");
    box.innerHTML = "";
    if (!vlans.length) {
      var hint = document.createElement("small");
      hint.textContent = "No VLANs yet: add them under “VLAN tags” or import from inventory.";
      box.appendChild(hint);
      return;
    }
    vlans.forEach(function (v) {
      var lab = document.createElement("label");
      var cb = document.createElement("input");
      cb.type = "checkbox";
      cb.checked = !!(n.vlans && n.vlans.indexOf(v.num) >= 0);
      cb.addEventListener("change", function () {
        var cur = (n.vlans || []).filter(function (x) { return x !== v.num; });
        if (cb.checked) {
          if (cur.length >= MAX_NODE_VLANS) {
            cb.checked = false;
            setStatus("A node can carry at most " + MAX_NODE_VLANS + " VLAN tags.", true);
            return;
          }
          cur.push(v.num);
        }
        cur.sort(function (a, b) { return a - b; });
        if (cur.length) n.vlans = cur; else delete n.vlans;
        setDirty(true);
        render();
      });
      var chip = document.createElement("span");
      chip.className = "chip";
      chip.style.background = vlanColor(v.num);
      chip.textContent = chipText(v.num);
      lab.appendChild(cb);
      lab.appendChild(chip);
      if (v.name) lab.appendChild(document.createTextNode(" " + v.name));
      box.appendChild(lab);
    });
  }

  $("prop-detail").addEventListener("input", function () {
    if (!selected || selected.type !== "node") return;
    var n = byId(nodes, selected.id);
    if (!n) return;
    var v = this.value.slice(0, MAX_LABEL);
    if (v.trim()) n.detail = v; else delete n.detail;
    setDirty(true);
    render();
  });

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
    var x = clamp(snap(p.x - drag.dx), 0, 5000 - nodeW(n));
    var y = clamp(snap(p.y - drag.dy), 0, 5000 - nodeH(n));
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
  var TIER_Y = 60, TIER_STEP = 170;
  function tierOf(n) {
    var k = kindMap[n.kind];
    return k && typeof k.tier === "number" ? k.tier : 3;
  }

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
    group("VLANs (added as tags)", "vlans", inventory.vlans);
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

  function invVlanNum(id) {
    var v = inventory.vlans.filter(function (x) { return x.id === id; })[0];
    return v ? v.num : 0;
  }
  function invVlanName(num) {
    var v = inventory.vlans.filter(function (x) { return x.num === num; })[0];
    return v ? v.name || "" : "";
  }

  // VLANs become tags on devices (never boxes). Services are their own nodes,
  // but logical ones: dashed border, dashed link to the device they run on.
  function addInventoryNode(type, item) {
    if (type === "vlans") return addVlanDef(item.num, item.name) ? true : null;
    if (hasRef(type, item.id)) return null;
    var tags = [];
    if (type === "devices") {
      tags = item.vlans.map(invVlanNum).filter(Boolean);
    } else if (type === "services" && item.vlan) {
      tags = [invVlanNum(item.vlan)].filter(Boolean);
    }
    tags = tags.slice(0, MAX_NODE_VLANS);
    tags.forEach(function (num) { addVlanDef(num, invVlanName(num)); });
    var kind = type === "services" ? "service" : item.kind || "server";
    var n = addNode(kind, item.label, { type: type, id: item.id }, TIER_Y + (kindMap[kind] ? kindMap[kind].tier : 3) * TIER_STEP);
    if (item.detail) n.detail = item.detail.slice(0, MAX_LABEL);
    if (tags.length) n.vlans = tags.sort(function (a, b) { return a - b; });
    return n;
  }

  function connect(a, b, style) {
    if (!a || !b) return null;
    var exists = links.some(function (l) {
      return (l.from === a.id && l.to === b.id) || (l.from === b.id && l.to === a.id);
    });
    if (exists) return null;
    var l = addLink(a.id, b.id);
    if (l && style) l.style = style;
    return l;
  }

  // Services get a dashed logical link to their host device. Device-to-switch
  // connections become one physical link per pair, labeled
  // "Trunk: 10,30 (native 1)" or "Access: 10" and colored by mode. Several
  // cables between the same pair share a link with combined labels.
  function linkInventory() {
    inventory.services.forEach(function (sv) {
      var sn = findRef("services", sv.id);
      if (sn && sv.host && findRef("devices", sv.host)) connect(sn, findRef("devices", sv.host), "logical");
    });
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
    if (n === true) {
      setStatus("Added " + chipText(item.num) + " to the VLAN tags. Tick it on a device to show it there.");
      syncProps();
      render();
    } else {
      select({ type: "node", id: n.id });
    }
  });

  $("btn-inv-all").addEventListener("click", function () {
    var added = 0;
    ["vlans", "devices", "services"].forEach(function (type) {
      inventory[type].forEach(function (it) { if (addInventoryNode(type, it)) added++; });
    });
    linkInventory();
    var anchored = autoLayout();
    setStatus(added ? "Imported " + added + " item(s)" + (anchored ? " and added an Internet node" : "") +
      " in tiers, top to bottom. Drag to fine-tune, then save." : "Everything is already on the diagram.");
    syncProps();
    render();
  });

  // ---- layout: top-to-bottom tiers with an Internet anchor ----------------
  // Adds an Internet node above the edge gear when the diagram has none, so a
  // reader has a starting point. Returns true when it added one.
  function ensureInternet() {
    if (nodes.some(function (n) { return n.kind === "internet"; })) return false;
    var deg = {};
    links.forEach(function (l) { deg[l.from] = (deg[l.from] || 0) + 1; deg[l.to] = (deg[l.to] || 0) + 1; });
    function best(kind) {
      var c = nodes.filter(function (n) { return n.kind === kind; });
      c.sort(function (a, b) { return (deg[b.id] || 0) - (deg[a.id] || 0); });
      return c[0] || null;
    }
    var edge = best("firewall") || best("router");
    if (!edge) return false;
    var net = addNode("internet", "Internet", null, TIER_Y);
    var l = addLink(net.id, edge.id);
    if (l) l.label = "WAN";
    return true;
  }

  // Tiers come from each node type (internet, edge gear, switches, hosts,
  // services). Within a tier, nodes are ordered by where their neighbors sit to
  // keep lines short; long tiers wrap onto extra rows.
  function autoLayout() {
    var anchored = ensureInternet();
    if (!nodes.length) return anchored;
    // Services are laid out separately, in a grid under their host, so a device
    // running 10+ of them stays readable.
    var svcNodes = nodes.filter(function (n) { return n.kind === "service"; });
    var tiers = {};
    nodes.forEach(function (n) {
      if (n.kind !== "service") (tiers[tierOf(n)] = tiers[tierOf(n)] || []).push(n);
    });
    var keys = Object.keys(tiers).map(Number).sort(function (a, b) { return a - b; });
    keys.forEach(function (t) { tiers[t].sort(function (a, b) { return a.x - b.x; }); });

    var adj = {};
    links.forEach(function (l) {
      var a = byId(nodes, l.from), b = byId(nodes, l.to);
      if (!a || !b || a.kind === "service" || b.kind === "service") return;
      (adj[l.from] = adj[l.from] || []).push(l.to);
      (adj[l.to] = adj[l.to] || []).push(l.from);
    });
    var pos = {};
    function indexTiers() {
      keys.forEach(function (t) {
        tiers[t].forEach(function (n, i) { pos[n.id] = (i + 0.5) / tiers[t].length; });
      });
    }
    function sweep(order) {
      order.forEach(function (t) {
        var bary = {};
        tiers[t].forEach(function (n) {
          var nb = (adj[n.id] || []).filter(function (id) { var m = byId(nodes, id); return m && tierOf(m) !== t; });
          bary[n.id] = nb.length ? nb.reduce(function (s, id) { return s + pos[id]; }, 0) / nb.length : pos[n.id];
        });
        tiers[t].sort(function (a, b) { return bary[a.id] - bary[b.id]; });
        tiers[t].forEach(function (n, i) { pos[n.id] = (i + 0.5) / tiers[t].length; });
      });
    }
    indexTiers();
    for (var i = 0; i < 3; i++) { sweep(keys.slice()); sweep(keys.slice().reverse()); }

    var GAPX = 50, GAPY = 90, MAXW = 1800;
    var rows = [];
    keys.forEach(function (t) {
      var cur = [], curW = 0;
      tiers[t].forEach(function (n) {
        var w = nodeW(n);
        if (cur.length && curW + GAPX + w > MAXW) { rows.push(cur); cur = []; curW = 0; }
        curW += (cur.length ? GAPX : 0) + w;
        cur.push(n);
      });
      if (cur.length) rows.push(cur);
    });
    function rowW(r) { return r.reduce(function (s, n, i) { return s + (i ? GAPX : 0) + nodeW(n); }, 0); }
    var widest = rows.reduce(function (m, r) { return Math.max(m, rowW(r)); }, 0);
    var mid = Math.max(widest + 80, 1000) / 2;
    var y = TIER_Y;
    rows.forEach(function (r) {
      var x = mid - rowW(r) / 2;
      var h = 0;
      r.forEach(function (n) {
        n.x = Math.max(40, snap(x));
        n.y = snap(y);
        x += nodeW(n) + GAPX;
        h = Math.max(h, nodeH(n));
      });
      y += h + GAPY;
    });
    placeServices(svcNodes, y);
    setDirty(true);
    return anchored;
  }

  // Each host's services form a block (up to 4 columns) centered under it;
  // blocks are packed left to right and wrap to a new band when the row is full.
  function placeServices(list, y0) {
    if (!list.length) return;
    var COLS = 4, GX = 30, GY = 24, BLOCK_GAP = 60, ROW_MAX = 2200;
    var groups = {}, blocks = [], loose = [];
    list.forEach(function (sv) {
      var host = null;
      links.forEach(function (l) {
        if (host || (l.from !== sv.id && l.to !== sv.id)) return;
        var o = byId(nodes, l.from === sv.id ? l.to : l.from);
        if (o && o.kind !== "service" && o.kind !== "vlan") host = o;
      });
      if (host) (groups[host.id] = groups[host.id] || { host: host, items: [] }).items.push(sv);
      else loose.push(sv);
    });
    Object.keys(groups).forEach(function (k) { blocks.push(groups[k]); });
    blocks.sort(function (a, b) { return a.host.x - b.host.x; });
    if (loose.length) blocks.push({ host: null, items: loose });
    blocks.forEach(function (b) {
      b.items.sort(function (p, q) { return p.label.localeCompare(q.label); });
      b.cols = Math.min(COLS, b.items.length);
      b.cw = b.items.reduce(function (m, n) { return Math.max(m, nodeW(n)); }, 0);
      b.rh = b.items.reduce(function (m, n) { return Math.max(m, nodeH(n)); }, 0);
      b.w = b.cols * b.cw + (b.cols - 1) * GX;
      b.h = Math.ceil(b.items.length / b.cols) * b.rh + (Math.ceil(b.items.length / b.cols) - 1) * GY;
    });
    var x = 40, y = y0, bandH = 0;
    blocks.forEach(function (b) {
      var want = b.host ? b.host.x + nodeW(b.host) / 2 - b.w / 2 : x;
      var bx = Math.max(x, want);
      if (x > 40 && bx + b.w > ROW_MAX) { y += bandH + GY * 2; bandH = 0; x = 40; bx = Math.max(x, want); }
      b.items.forEach(function (sv, i) {
        var c = i % b.cols, r = Math.floor(i / b.cols);
        sv.x = Math.max(40, snap(bx + c * (b.cw + GX) + (b.cw - nodeW(sv)) / 2));
        sv.y = snap(y + r * (b.rh + GY));
      });
      x = bx + b.w + BLOCK_GAP;
      bandH = Math.max(bandH, b.h);
    });
  }

  $("btn-layout").addEventListener("click", function () {
    var anchored = autoLayout();
    setStatus("Arranged top to bottom: Internet → firewall/router → switches → hosts → services." +
      (anchored ? " Added an Internet node as the starting point." : ""));
    render();
  });

  $("btn-labels").addEventListener("click", function () {
    hideLabels = !hideLabels;
    setDirty(true);
    render();
  });

  // ---- old diagrams: VLANs drawn as boxes ---------------------------------
  function legacyVlanNodes() { return nodes.filter(function (n) { return n.kind === "vlan"; }); }

  function vlanNumOfNode(n) {
    if (n.ref && n.ref.type === "vlans") {
      var num = invVlanNum(n.ref.id);
      if (num) return num;
    }
    var m = /(\d{1,4})/.exec(n.label);
    var v = m ? Number(m[1]) : 0;
    return v >= 1 && v <= 4094 ? v : 0;
  }

  // Each VLAN box becomes a tag on the devices it was linked to, then goes away.
  function convertVlanNodes() {
    var done = 0;
    legacyVlanNodes().forEach(function (vn) {
      var num = vlanNumOfNode(vn);
      if (!num) return;
      var name = vn.label.replace(/^\s*VLAN\s*\d+\s*[-–—:]?\s*/i, "").trim() || invVlanName(num);
      addVlanDef(num, name);
      links.forEach(function (l) {
        if (l.from !== vn.id && l.to !== vn.id) return;
        var other = byId(nodes, l.from === vn.id ? l.to : l.from);
        if (!other || other.kind === "vlan") return;
        var cur = other.vlans || [];
        if (cur.indexOf(num) < 0 && cur.length < MAX_NODE_VLANS) {
          other.vlans = cur.concat(num).sort(function (a, b) { return a - b; });
        }
      });
      nodes = nodes.filter(function (n) { return n.id !== vn.id; });
      links = links.filter(function (l) { return l.from !== vn.id && l.to !== vn.id; });
      done++;
    });
    if (selected && !byId(nodes, selected.id) && !byId(links, selected.id)) selected = null;
    setDirty(true);
    setStatus(done ? "Converted " + done + " VLAN box" + (done === 1 ? "" : "es") + " into tags on the devices." : "Could not read a VLAN number from those boxes.", !done);
    syncProps();
    render();
  }
  $("btn-convert").addEventListener("click", convertVlanNodes);

  function updateLegacyNote() {
    var n = legacyVlanNodes().length;
    $("legacy-note").hidden = !n;
    if (n) {
      $("legacy-msg").textContent = n + (n === 1 ? " VLAN is" : " VLANs are") +
        " drawn as a box with lines, which reads like a device you plug into. Tags on the devices show the same thing without implying a cable.";
    }
  }

  // ---- save / export / share ---------------------------------------------
  function save() {
    setStatus("Saving…");
    return fetch("/topologies/" + topoId + "/save", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: nameInput.value, nodes: nodes, links: links, vlans: vlans, hideLabels: hideLabels }),
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
