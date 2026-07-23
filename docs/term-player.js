// Terminal replay player for frugal.sh. Plays pre-recorded CLI sessions
// (real captured runs — see the JSON block in index.html) with a typing
// effect. No dependencies, no network. Honors prefers-reduced-motion by
// rendering each recording's final frame instantly.
(function () {
  'use strict';

  var container = document.getElementById('term-player');
  var dataEl = document.getElementById('term-recordings');
  if (!container || !dataEl) return;

  var recordings;
  try { recordings = JSON.parse(dataEl.textContent); } catch (_) { return; }
  if (!recordings || !recordings.length) return;

  var reduced = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;

  // Event model per recording line:
  //   {t:"cmd",  s:"curl ..."}   green "$ " prompt, then typed char by char
  //   {t:"call", s:"frugal__…"}  muted "› " prompt (an MCP call, not a shell), typed
  //   {t:"out",  s:"...", c:"m|g|a"}  output line, per-line delay; c = color class
  //   {t:"wait", ms:600}         pause
  var TYPE_MS = 28;      // per character
  var LINE_MS = 90;      // per output line
  var esc = function (s) {
    return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  };

  // ── chrome ──
  container.className = 'term-player';
  var bar = document.createElement('div');
  bar.className = 'tp-bar';
  bar.innerHTML =
    '<div class="tp-dots"><span class="tp-dot r"></span><span class="tp-dot y"></span><span class="tp-dot g"></span></div>' +
    '<span class="tp-title">🎺 frugal — real captured runs</span>' +
    '<button type="button" class="tp-restart" aria-label="Replay">↻ replay</button>';
  container.appendChild(bar);

  var tabs = document.createElement('div');
  tabs.className = 'tp-tabs';
  tabs.setAttribute('role', 'tablist');
  recordings.forEach(function (rec, i) {
    var b = document.createElement('button');
    b.className = 'tp-tab';
    b.setAttribute('role', 'tab');
    b.setAttribute('aria-selected', i === 0 ? 'true' : 'false');
    b.textContent = rec.title;
    b.addEventListener('click', function () { play(i); });
    tabs.appendChild(b);
  });
  container.appendChild(tabs);

  var screen = document.createElement('div');
  screen.className = 'tp-screen';
  var pre = document.createElement('pre');
  pre.setAttribute('aria-live', 'off');
  screen.appendChild(pre);
  container.appendChild(screen);

  var active = 0;
  var timer = null;
  var html = '';

  function setTabs(i) {
    Array.prototype.forEach.call(tabs.children, function (el, j) {
      el.setAttribute('aria-selected', i === j ? 'true' : 'false');
    });
  }

  function render(cursor) {
    pre.innerHTML = html + (cursor ? '<span class="tp-cursor"></span>' : '');
  }

  function lineHTML(ev) {
    var cls = ev.c ? 'c-' + ev.c : '';
    var body = esc(ev.s);
    return cls ? '<span class="' + cls + '">' + body + '</span>' : body;
  }

  function promptHTML(ev) {
    return ev.t === 'call' ? '<span class="c-m">› </span>' : '<span class="c-p">$ </span>';
  }

  function finalFrame(rec) {
    var out = '';
    rec.events.forEach(function (ev) {
      if (ev.t === 'cmd' || ev.t === 'call') out += promptHTML(ev) + esc(ev.s) + '\n';
      else if (ev.t === 'out') out += lineHTML(ev) + '\n';
    });
    return out;
  }

  function play(i) {
    active = i;
    setTabs(i);
    clearTimeout(timer);
    html = '';
    if (reduced) {
      html = finalFrame(recordings[i]);
      render(false);
      return;
    }
    render(true);
    var events = recordings[i].events;
    var e = 0, ch = 0;

    function step() {
      if (active !== i) return;
      if (e >= events.length) { render(false); return; }
      var ev = events[e];
      if (ev.t === 'cmd' || ev.t === 'call') {
        if (ch === 0) html += promptHTML(ev);
        if (ch < ev.s.length) {
          html += esc(ev.s.charAt(ch));
          ch++;
          render(true);
          timer = setTimeout(step, TYPE_MS);
          return;
        }
        html += '\n';
        ch = 0; e++;
        render(true);
        timer = setTimeout(step, 220);
        return;
      }
      if (ev.t === 'out') {
        html += lineHTML(ev) + '\n';
        e++;
        render(true);
        timer = setTimeout(step, LINE_MS);
        return;
      }
      if (ev.t === 'wait') {
        e++;
        timer = setTimeout(step, ev.ms || 400);
        return;
      }
      e++;
      step();
    }
    step();
  }

  bar.querySelector('.tp-restart').addEventListener('click', function () { play(active); });

  // Autoplay the first tab when the player scrolls into view.
  if (!reduced && 'IntersectionObserver' in window) {
    html = '';
    render(true);
    var started = false;
    var io = new IntersectionObserver(function (entries) {
      entries.forEach(function (en) {
        if (en.isIntersecting && !started) { started = true; play(0); io.disconnect(); }
      });
    }, { threshold: 0.3 });
    io.observe(container);
  } else {
    play(0);
  }
})();
