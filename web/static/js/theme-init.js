// Runs synchronously in <head> before first paint: applies the stored theme and marks JS support.
(function () {
  var root = document.documentElement;
  root.classList.add('js');
  try {
    var t = localStorage.getItem('nwr_theme');
    if (t === 'light' || t === 'dark') root.setAttribute('data-theme', t);
  } catch (e) { /* storage blocked: follow the system theme */ }
})();
