// Rasterizes a server-rendered diagram SVG to a PNG download, entirely in the
// browser (no server-side image dependency).
window.LabDocExport = {
  png: function (svgUrl, filename) {
    return fetch(svgUrl, { cache: "no-store" })
      .then(function (r) {
        if (!r.ok) throw new Error("image request failed (" + r.status + ")");
        return r.text();
      })
      .then(function (text) {
        var vb = new DOMParser()
          .parseFromString(text, "image/svg+xml")
          .documentElement.getAttribute("viewBox");
        if (!vb) throw new Error("image has no size");
        var parts = vb.split(/\s+/).map(Number);
        var w = parts[2], h = parts[3], scale = 2;
        var url = URL.createObjectURL(new Blob([text], { type: "image/svg+xml" }));
        return new Promise(function (resolve, reject) {
          var img = new Image();
          img.onload = function () {
            var c = document.createElement("canvas");
            c.width = Math.round(w * scale);
            c.height = Math.round(h * scale);
            var ctx = c.getContext("2d");
            ctx.fillStyle = "#fff";
            ctx.fillRect(0, 0, c.width, c.height);
            ctx.drawImage(img, 0, 0, c.width, c.height);
            URL.revokeObjectURL(url);
            c.toBlob(function (blob) {
              if (!blob) { reject(new Error("PNG encoding failed")); return; }
              var a = document.createElement("a");
              a.href = URL.createObjectURL(blob);
              a.download = filename;
              document.body.appendChild(a);
              a.click();
              a.remove();
              setTimeout(function () { URL.revokeObjectURL(a.href); }, 1000);
              resolve();
            }, "image/png");
          };
          img.onerror = function () {
            URL.revokeObjectURL(url);
            reject(new Error("could not render the image"));
          };
          img.src = url;
        });
      });
  },
};
