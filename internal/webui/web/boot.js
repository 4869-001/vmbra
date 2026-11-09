/* eslint-disable */
(function () {
  "use strict";
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", function () { window.App.boot(); });
  } else {
    window.App.boot();
  }
})();
