// DOM, fetch, commands and keyboard handling for the RepoPilot terminal. Pure logic lives in core.js.
// Everything shown comes from the API or the user, and repository content is attacker-influenced, so text is only ever
// set through textContent or text nodes.
(function () {
  "use strict";

  var Core = window.RPCore;
  var logEl = document.getElementById("log");
  var input = document.getElementById("cmd");
  var promptEl = document.getElementById("prompt");
  var form = document.getElementById("prompt-form");

  var HISTORY_KEY = "repopilot.history";
  var HISTORY_LIMIT = 100;
  var POLL_MS = 2000;
  var QUEUED_HINT_MS = 10000;
  var WATCH_MAX_MS = 30 * 60 * 1000;
  var MAX_POLL_FAILS = 5;

  var state = {
    active: null, // {id, owner, name, status, commit}
    citations: [], // citations of the last answer
    history: [],
    histIdx: 0,
    draft: "",
    watch: null,
    asking: false,
  };

  // ---- output ----

  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }

  function scrollToEnd() {
    window.scrollTo(0, document.documentElement.scrollHeight);
  }

  function append(node) {
    logEl.appendChild(node);
    scrollToEnd();
    return node;
  }

  function print(text, cls) {
    return append(el("div", "line" + (cls ? " " + cls : ""), text));
  }

  function printError(err) {
    Core.errorLines(err).forEach(function (l, i) {
      print(l, i === 0 ? "err" : "dim");
    });
  }

  function promptText() {
    return "repopilot ~ " + (state.active ? Core.repoLabel(state.active) : "(no repo)") + " ▸";
  }

  function echo(text) {
    var line = el("div", "line echo");
    line.appendChild(el("span", "echo-prompt", promptText() + " "));
    line.appendChild(document.createTextNode(text));
    append(line);
  }

  function renderAnswer(container, segments, onCite) {
    segments.forEach(function (s) {
      if (s.type === "cite") {
        var b = el("button", "cite", s.text);
        b.type = "button";
        b.setAttribute("aria-label", "show citation " + s.n);
        b.addEventListener("click", function () {
          onCite(s.n);
        });
        container.appendChild(b);
      } else {
        container.appendChild(document.createTextNode(s.text));
      }
    });
  }

  function renderSnippet(container, lines) {
    container.appendChild(el("pre", "snippet", lines.join("\n")));
  }

  function setActive(r) {
    var changed = !state.active || state.active.id !== r.id;
    state.active = { id: r.id, owner: r.owner, name: r.name, status: r.status, commit: r.commit_sha };
    if (changed) state.citations = [];
    promptEl.textContent = promptText();
  }

  // ---- API ----

  async function api(method, path, body) {
    var opts = { method: method, headers: { Accept: "application/json" } };
    if (body !== undefined) {
      opts.headers["Content-Type"] = "application/json";
      opts.body = JSON.stringify(body);
    }
    var res;
    try {
      res = await fetch(path, opts);
    } catch (e) {
      return { network: true, error: Core.networkError() };
    }
    var data = null;
    try {
      data = await res.json();
    } catch (e) {
      data = null;
    }
    if (!res.ok || data === null) return { status: res.status, error: Core.toError(res.status, data) };
    return { status: res.status, data: data };
  }

  async function health() {
    try {
      var res = await fetch("/healthz", { headers: { Accept: "application/json" } });
      var d = await res.json();
      return res.ok && d && d.status === "ok" ? "ok" : "database down";
    } catch (e) {
      return "unreachable";
    }
  }

  async function loadList() {
    var r = await api("GET", "/api/repositories");
    if (r.error) return r;
    if (!Array.isArray(r.data)) return { error: Core.toError(r.status, null) };
    return r;
  }

  // ---- watching an indexing job ----

  function stopWatch(message) {
    var w = state.watch;
    if (!w) return false;
    w.cancelled = true;
    if (w.wake) w.wake();
    state.watch = null;
    if (message) print(message, "dim");
    return true;
  }

  async function watch(id, initial) {
    stopWatch();
    var w = { cancelled: false, wake: null };
    state.watch = w;
    var start = Date.now();
    var queuedSince = start;
    var hinted = false;
    var fails = 0;
    var lastKey = Core.progressKey(initial);

    function done() {
      if (state.watch === w) state.watch = null;
    }

    while (!w.cancelled) {
      var r = await api("GET", "/api/repositories/" + id);
      if (w.cancelled) return;
      if (r.error) {
        if (r.network || r.status >= 500) {
          fails++;
          if (fails >= MAX_POLL_FAILS) {
            print("error: network: cannot reach the API", "err");
            done();
            return;
          }
        } else {
          printError(r.error);
          done();
          return;
        }
      } else {
        fails = 0;
        var d = r.data;
        if (d.status === "ready") {
          print(Core.progressLine(d), "ok");
          setActive(d);
          done();
          return;
        }
        if (d.status === "failed") {
          print("error: indexing failed: " + (d.error || "unknown error"), "err");
          done();
          return;
        }
        var key = Core.progressKey(d);
        if (key !== lastKey) {
          print(Core.progressLine(d));
          lastKey = key;
        }
        if (d.status === "queued") {
          if (!hinted && Date.now() - queuedSince >= QUEUED_HINT_MS) {
            hinted = true;
            print("still queued: is the worker running? python -m repopilot_worker.main", "warn");
          }
        } else {
          queuedSince = Date.now();
        }
      }
      if (Date.now() - start > WATCH_MAX_MS) {
        print("stopped watching after 30 minutes (indexing may still be running; run 'status')", "warn");
        done();
        return;
      }
      await new Promise(function (resolve) {
        var t = setTimeout(resolve, POLL_MS);
        w.wake = function () {
          clearTimeout(t);
          resolve();
        };
      });
    }
  }

  // ---- commands ----

  async function cmdHelp(args) {
    if (args.length > 1) return print(Core.usageError("help"), "err");
    if (args.length === 1) {
      var c = Core.findCommand(args[0]);
      if (!c) return print("error: unknown_command: no command named '" + args[0] + "'. Run 'help' for the list", "err");
      print("usage: " + c.usage);
      print(c.summary);
      c.detail.forEach(function (l) {
        print(l, "dim");
      });
      return;
    }
    Core.formatTable(
      Core.COMMANDS.map(function (c) {
        return [c.usage, c.summary];
      })
    ).forEach(function (l) {
      print(l);
    });
    print("");
    print("Bare text is a question. A question that starts with a command word needs the 'ask' prefix.", "dim");
  }

  async function cmdAdd(args) {
    if (args.length !== 1) return print(Core.usageError("add"), "err");
    var r = await api("POST", "/api/repositories", { url: args[0] });
    if (r.error) return printError(r.error);
    var id = r.data.id;
    var g = await api("GET", "/api/repositories/" + id);
    if (g.error) return printError(g.error);
    var d = g.data;
    var label = Core.repoLabel(d);
    if (!r.data.created && !r.data.requeued) {
      print("already added: repo " + id + " " + label + " (" + d.status + ")");
      setActive(d);
      if (d.status === "queued" || d.status === "indexing") startWatch(id, d);
      return;
    }
    print("[queued] repo " + id + " " + label);
    startWatch(id, d);
  }

  function startWatch(id, initial) {
    watch(id, initial).catch(function (e) {
      console.error(e);
      print("error: internal: stopped watching (" + (e && e.message ? e.message : "unexpected failure") + ")", "err");
    });
  }

  async function cmdRepos() {
    var r = await loadList();
    if (r.error) return printError(r.error);
    if (r.data.length === 0) return print("no repositories yet. Try: add https://github.com/<owner>/<repo>");
    var rows = [[" ", "id", "status", "repository", "commit", "created"]];
    r.data.forEach(function (x) {
      var mark = state.active && state.active.id === x.id ? "*" : " ";
      rows.push([mark, String(x.id), x.status, Core.repoLabel(x), Core.shortSha(x.commit_sha), Core.formatDate(x.created_at)]);
    });
    Core.formatTable(rows).forEach(function (l, i) {
      print(l, i === 0 ? "dim" : "");
    });
  }

  async function cmdUse(args) {
    if (args.length !== 1) return print(Core.usageError("use"), "err");
    var r = await loadList();
    if (r.error) return printError(r.error);
    var found = Core.findRepo(r.data, args[0]);
    if (!found) return print("error: not_found: no repository matches '" + args[0] + "'. Run 'repos' to list them", "err");
    setActive(found);
    print("using " + Core.repoLabel(found) + (found.commit_sha ? " @ " + Core.shortSha(found.commit_sha) : ""));
    if (found.status !== "ready") print("warning: repository is " + found.status + ", questions need ready", "warn");
  }

  async function cmdStatus(args) {
    if (args.length > 1) return print(Core.usageError("status"), "err");
    var id;
    if (args.length === 0) {
      if (!state.active) return print("error: no_repo: run 'use <id>' first", "err");
      id = state.active.id;
    } else if (/^\d+$/.test(args[0])) {
      id = parseInt(args[0], 10);
    } else {
      var l = await loadList();
      if (l.error) return printError(l.error);
      var f = Core.findRepo(l.data, args[0]);
      if (!f) return print("error: not_found: no repository matches '" + args[0] + "'. Run 'repos' to list them", "err");
      id = f.id;
    }
    var r = await api("GET", "/api/repositories/" + id);
    if (r.error) return printError(r.error);
    var d = r.data;
    var rows = [
      ["repo:", id + " " + Core.repoLabel(d)],
      ["status:", d.status],
      ["commit:", Core.shortSha(d.commit_sha)],
    ];
    if (Core.phaseText(d)) rows.push(["phase:", Core.phaseText(d)]);
    if (d.error) rows.push(["error:", d.error]);
    Core.formatTable(rows).forEach(function (l) {
      print(l);
    });
    if (state.active && state.active.id === d.id) setActive(d);
  }

  function renderResponse(a) {
    if (!a.grounded && !a.refused) print("warning: answer has no verified citations", "warn");
    if (a.truncated) print("warning: answer was cut off (output limit reached)", "warn");
    if (a.refused) print("[refused]", "dim");
    var box = el("div", "line answer" + (a.refused ? " refusal" : ""));
    renderAnswer(
      box,
      Core.splitAnswer(
        a.answer,
        a.citations.map(function (c) {
          return c.n;
        })
      ),
      function (n) {
        runLine("show " + n);
      }
    );
    append(box);
    state.citations = a.citations;
    if (a.citations.length > 0) {
      print("");
      print("sources:");
      Core.sourceLines(a.citations).forEach(function (l) {
        print(l);
      });
      print("type 'show <n>' to view a snippet", "dim");
    }
    print(Core.formatStats(a.stats), "dim");
    var note = Core.rerankNote(a.stats);
    if (note) print(note, "dim");
  }

  async function cmdAsk(text) {
    var opts = Core.parseAskOptions(text);
    if (opts.error) return print("error: " + opts.error + "; " + Core.usageError("ask").replace(/^error: /, ""), "err");
    var question = opts.question;
    if (question === "") return print(Core.usageError("ask"), "err");
    if (!state.active) return print("error: no_repo: run 'use <id>' first", "err");
    if (state.asking) return print("error: busy: wait for the previous answer", "err");
    var id = state.active.id;
    var body = { question: question };
    if (opts.filters) body.filters = opts.filters;
    state.asking = true;
    var pending = print("asking...", "dim");
    var r;
    try {
      r = await api("POST", "/api/repositories/" + id + "/query", body);
    } finally {
      state.asking = false;
      pending.remove();
    }
    if (r.error) return printError(r.error);
    var a = Core.normaliseAnswer(r.data);
    if (!a) return printError(Core.toError(r.status, null));
    if (!state.active || state.active.id !== id) return print("error: cancelled: the active repository changed while waiting", "err");
    var fl = Core.filterLine(opts.filters);
    if (fl) print(fl, "dim");
    renderResponse(a);
  }

  async function cmdShow(args) {
    if (args.length !== 1 || !/^\d+$/.test(args[0])) return print(Core.usageError("show"), "err");
    var n = parseInt(args[0], 10);
    var c = null;
    state.citations.forEach(function (x) {
      if (x.n === n) c = x;
    });
    if (!c) return print("error: no citation [" + n + "] in the last answer", "err");
    print(Core.snippetHeader(c));
    var box = el("div", "line");
    renderSnippet(box, Core.formatSnippet(c.snippet, c.start_line));
    append(box);
  }

  async function cmdClear() {
    logEl.textContent = "";
  }

  var commands = {
    help: cmdHelp,
    add: cmdAdd,
    repos: cmdRepos,
    use: cmdUse,
    status: cmdStatus,
    show: cmdShow,
    clear: cmdClear,
  };

  async function dispatch(parsed) {
    if (parsed.kind === "empty") return;
    if (parsed.kind === "ask") return cmdAsk(parsed.question);
    return commands[parsed.name](parsed.args, parsed.rest);
  }

  async function runLine(text) {
    var parsed = Core.parseInput(text);
    if (parsed.kind === "empty") return;
    if (!(parsed.kind === "command" && parsed.name === "clear")) echo(text.trim());
    try {
      await dispatch(parsed);
    } catch (e) {
      console.error(e);
      print("error: internal: " + (e && e.message ? e.message : "unexpected failure"), "err");
    }
  }

  // ---- history ----

  function loadHistory() {
    try {
      var raw = window.localStorage.getItem(HISTORY_KEY);
      var list = raw ? JSON.parse(raw) : [];
      if (Array.isArray(list)) {
        state.history = list.filter(function (x) {
          return typeof x === "string";
        }).slice(-HISTORY_LIMIT);
      }
    } catch (e) {
      state.history = [];
    }
    state.histIdx = state.history.length;
  }

  function saveHistory() {
    try {
      window.localStorage.setItem(HISTORY_KEY, JSON.stringify(state.history));
    } catch (e) {
      // storage unavailable: history lasts only for this page
    }
  }

  // ---- keyboard ----

  form.addEventListener("submit", function (ev) {
    ev.preventDefault();
    var value = input.value;
    input.value = "";
    if (value.trim() === "") return;
    state.history = Core.pushHistory(state.history, value, HISTORY_LIMIT);
    state.histIdx = state.history.length;
    state.draft = "";
    saveHistory();
    runLine(value);
  });

  input.addEventListener("keydown", function (ev) {
    if (ev.key === "ArrowUp" || ev.key === "ArrowDown") {
      if (state.history.length === 0) return;
      ev.preventDefault();
      if (state.histIdx === state.history.length) state.draft = input.value;
      state.histIdx = Core.historyMove(state.history.length, state.histIdx, ev.key === "ArrowUp" ? -1 : 1);
      input.value = state.histIdx === state.history.length ? state.draft : state.history[state.histIdx];
      input.setSelectionRange(input.value.length, input.value.length);
    } else if (ev.ctrlKey && (ev.key === "l" || ev.key === "L")) {
      ev.preventDefault();
      logEl.textContent = "";
    } else if (ev.ctrlKey && (ev.key === "c" || ev.key === "C")) {
      var selected = input.selectionStart !== input.selectionEnd || String(window.getSelection()) !== "";
      if (selected) return; // let the browser copy
      ev.preventDefault();
      if (!stopWatch("stopped watching (indexing continues in the background)")) {
        echo(input.value + "^C");
        input.value = "";
      }
    }
  });

  document.addEventListener("click", function (ev) {
    if (ev.target.closest && ev.target.closest("button")) return;
    if (String(window.getSelection()) !== "") return;
    input.focus();
  });

  // ---- start ----

  async function banner() {
    print("RepoPilot: ask questions about a GitHub repository and get answers that cite the code.");
    var h = await health();
    print("api: " + h, h === "ok" ? "ok" : "err");
    if (h === "unreachable") return printError(Core.networkError());
    var r = await loadList();
    if (r.error) return printError(r.error);
    var list = r.data;
    if (list.length === 0) {
      print("no repositories yet. Try: add https://github.com/<owner>/<repo>");
    } else if (list.length === 1 && list[0].status === "ready") {
      setActive(list[0]);
      print("using " + Core.repoLabel(list[0]) + " @ " + Core.shortSha(list[0].commit_sha) + " (the only repository)");
    } else {
      print(list.length + (list.length === 1 ? " repository." : " repositories.") + " Run 'repos', then 'use <id>'.");
    }
    print("type 'help' for commands", "dim");
  }

  window.RPUI = { print: print, renderAnswer: renderAnswer, renderSnippet: renderSnippet };

  if (window.location.hash === "#selftest") {
    var s = document.createElement("script");
    s.src = "/selftest.js";
    document.head.appendChild(s);
  } else {
    loadHistory();
    banner();
  }
  input.focus();
})();
