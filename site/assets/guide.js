// OpenLAMT user guide: light/dark toggle only. Every page reads fine without this script.
(function () {
  var root = document.documentElement;
  var key = 'openlamt-guide-theme';

  function stored() {
    try { return localStorage.getItem(key) || 'system'; } catch (e) { return 'system'; }
  }

  function apply(t) {
    if (t === 'light' || t === 'dark') root.setAttribute('data-theme', t);
    else root.removeAttribute('data-theme');
    var buttons = document.querySelectorAll('[data-theme-set]');
    for (var i = 0; i < buttons.length; i++) {
      buttons[i].setAttribute('aria-pressed', buttons[i].getAttribute('data-theme-set') === t ? 'true' : 'false');
    }
  }

  // Runs in <head>: set the theme before the page paints.
  apply(stored());

  document.addEventListener('DOMContentLoaded', function () {
    var bar = document.querySelector('.themebar');
    if (!bar) return;
    bar.hidden = false;
    apply(stored());
    bar.addEventListener('click', function (e) {
      var b = e.target.closest('[data-theme-set]');
      if (!b) return;
      var t = b.getAttribute('data-theme-set');
      try { localStorage.setItem(key, t); } catch (err) { /* private mode: keep it for this page */ }
      apply(t);
    });
  });
})();
