// Pure logic for the RepoPilot terminal: no DOM, no network. terminal.js does the I/O; selftest.js tests this file.
(function (root) {
  "use strict";

  var COMMANDS = [
    {
      name: "help",
      usage: "help [command]",
      summary: "list commands, or show usage for one",
      detail: ["Bare text (no command word) is a question. A question that starts with a command word needs the 'ask' prefix."],
    },
    {
      name: "add",
      usage: "add <github-url>",
      summary: "add a repository and watch it being indexed",
      detail: [
        "Example: add https://github.com/golang/example",
        "Indexing runs in a separate worker process. Ctrl+C stops watching; indexing continues.",
      ],
    },
    { name: "repos", usage: "repos", summary: "list repositories", detail: ["The active repository is marked with *."] },
    {
      name: "use",
      usage: "use <id|owner/name>",
      summary: "select the active repository",
      detail: ["Example: use 1   or   use golang/example"],
    },
    {
      name: "status",
      usage: "status [id|owner/name]",
      summary: "show status, commit, progress and errors",
      detail: ["Defaults to the active repository."],
    },
    {
      name: "ask",
      usage: "ask <question>",
      summary: "ask the active repository (the 'ask' word is optional)",
      detail: ["Answers cite the code as [n]. Click a [n] or run 'show n' to see the lines."],
    },
    {
      name: "show",
      usage: "show <n>",
      summary: "show the code behind citation [n] of the last answer",
      detail: [],
    },
    { name: "clear", usage: "clear", summary: "clear the screen (Ctrl+L too)", detail: [] },
  ];

  var HINTS = {
    repo_not_ready: "run 'status' to see the indexing progress",
    rate_limited: "wait a minute and try again",
    not_configured: "set the API keys in .env and restart the API",
    network: "is the API running?",
    timeout: "try again",
    upstream_error: "the model or embedding provider failed; try again in a moment",
    model_mismatch: "the repository was indexed with a different embedding model than the API uses",
    answer_blocked: "the model did not answer; rephrase the question",
  };

  function findCommand(name) {
    for (var i = 0; i < COMMANDS.length; i++) {
      if (COMMANDS[i].name === name) return COMMANDS[i];
    }
    return null;
  }

  // A line whose first word is exactly a command name runs that command; anything else is a question.
  function parseInput(line) {
    var text = String(line == null ? "" : line).trim();
    if (text === "") return { kind: "empty" };
    var m = /^(\S+)(?:\s+([\s\S]*))?$/.exec(text);
    var word = m[1];
    var rest = (m[2] || "").trim();
    if (findCommand(word) === null) return { kind: "ask", question: text };
    if (word === "ask") return { kind: "ask", question: rest };
    return { kind: "command", name: word, args: rest === "" ? [] : rest.split(/\s+/), rest: rest };
  }

  function padRight(s, n) {
    s = String(s);
    while (s.length < n) s += " ";
    return s;
  }

  function padLeft(s, n) {
    s = String(s);
    while (s.length < n) s = " " + s;
    return s;
  }

  // rows: array of arrays of strings. Columns are aligned with two spaces between them; the last column is not padded.
  function formatTable(rows) {
    var widths = [];
    rows.forEach(function (row) {
      row.forEach(function (cell, i) {
        widths[i] = Math.max(widths[i] || 0, String(cell).length);
      });
    });
    return rows.map(function (row) {
      return row
        .map(function (cell, i) {
          return i === row.length - 1 ? String(cell) : padRight(cell, widths[i]);
        })
        .join("  ");
    });
  }

  function shortSha(sha) {
    return typeof sha === "string" && sha.length > 0 ? sha.slice(0, 7) : "-";
  }

  function formatDate(iso) {
    var m = /^(\d{4}-\d{2}-\d{2})/.exec(String(iso == null ? "" : iso));
    return m ? m[1] : "-";
  }

  function formatCount(n) {
    return String(n).replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  }

  function isCount(v) {
    return typeof v === "number" && isFinite(v) && v >= 0;
  }

  function repoLabel(r) {
    return r.owner + "/" + r.name;
  }

  // The embedding phase counts chunks; every other counted phase counts files.
  function unitFor(phase) {
    return phase === "embedding" ? "chunks" : "files";
  }

  function progressOf(d) {
    var p = (d && d.progress) || {};
    return { phase: typeof p.phase === "string" ? p.phase : "", done: p.files_done, total: p.files_total };
  }

  // One line per status, phase or progress bucket. Returns null when there is nothing to print.
  function progressLine(d) {
    if (!d) return null;
    if (d.status === "queued") return "[queued]";
    if (d.status === "ready") return "[ready] " + repoLabel(d) + " @ " + shortSha(d.commit_sha);
    if (d.status === "failed") return "[failed] " + (d.error || "unknown error");
    var p = progressOf(d);
    if (p.phase === "") return "[indexing]";
    var line = "[" + p.phase + "]";
    if (p.phase === "scanning") {
      if (isCount(p.total)) line += " " + p.total + " files";
    } else if (p.phase === "chunking" || p.phase === "embedding") {
      if (isCount(p.total)) line += " " + (isCount(p.done) ? p.done : 0) + "/" + p.total + " " + unitFor(p.phase);
    }
    return line;
  }

  // 'embedding 96/248 chunks' for the status command; '' when there is no phase.
  function phaseText(d) {
    if (!d || progressOf(d).phase === "") return "";
    return progressLine({ status: "indexing", progress: d.progress }).replace(/^\[([a-z]+)\]/, "$1");
  }

  // Changes only when a new line is worth printing: counted phases move in 10% steps so a big job prints a bounded number of lines.
  function progressKey(d) {
    if (!d) return "";
    var p = progressOf(d);
    var bucket = "";
    if (p.phase === "scanning") {
      bucket = isCount(p.total) ? String(p.total) : "";
    } else if ((p.phase === "chunking" || p.phase === "embedding") && isCount(p.total)) {
      var done = isCount(p.done) ? p.done : 0;
      bucket = String(p.total > 0 ? Math.min(10, Math.floor((done * 10) / p.total)) : 0);
    }
    return [d.status, p.phase, bucket].join("|");
  }

  // Splits an answer into text and citation segments. A marker [n] counts only when n is a returned citation and it follows
  // whitespace, punctuation, or another citation marker, so 'items[1]' stays plain text.
  function splitAnswer(text, validNs) {
    text = String(text == null ? "" : text);
    var valid = {};
    (validNs || []).forEach(function (n) {
      valid[n] = true;
    });
    var re = /\[(\d{1,4})\]/g;
    var segs = [];
    var pos = 0;
    var lastCiteEnd = -1;
    var m;
    while ((m = re.exec(text)) !== null) {
      var n = parseInt(m[1], 10);
      var start = m.index;
      var prev = start > 0 ? text.charAt(start - 1) : "";
      var ok = valid[n] === true;
      if (ok && prev !== "") {
        if (prev === "]") ok = lastCiteEnd === start;
        else ok = /[\s\p{P}]/u.test(prev);
      }
      if (!ok) continue;
      if (start > pos) segs.push({ type: "text", text: text.slice(pos, start) });
      segs.push({ type: "cite", n: n, text: m[0] });
      pos = start + m[0].length;
      lastCiteEnd = pos;
    }
    if (pos < text.length) segs.push({ type: "text", text: text.slice(pos) });
    return segs;
  }

  // Lines of a snippet with their real line numbers: '  65 | code'. Tabs are kept.
  function formatSnippet(snippet, startLine) {
    var text = String(snippet == null ? "" : snippet).replace(/\r\n?/g, "\n");
    if (text.slice(-1) === "\n") text = text.slice(0, -1);
    var lines = text.split("\n");
    var first = isCount(startLine) ? startLine : 1;
    var width = String(first + lines.length - 1).length;
    return lines.map(function (l, i) {
      return padLeft(first + i, width) + " | " + l;
    });
  }

  function citationRef(c) {
    return c.file + ":" + c.start_line + "-" + c.end_line;
  }

  function snippetHeader(c) {
    var what = [c.kind, c.symbol].filter(function (s) {
      return typeof s === "string" && s !== "";
    });
    var meta = [c.language, what.join(" ")].filter(function (s) {
      return typeof s === "string" && s !== "";
    });
    return "[" + c.n + "] " + citationRef(c) + (meta.length ? " (" + meta.join(", ") + ")" : "");
  }

  function sourceLines(citations) {
    var rows = citations.map(function (c) {
      return ["  [" + c.n + "]", citationRef(c), c.symbol || ""];
    });
    return formatTable(rows).map(function (l) {
      return l.replace(/\s+$/, "");
    });
  }

  function formatStats(s) {
    s = s || {};
    var ms = (s.embed_ms || 0) + (s.search_ms || 0) + (s.llm_ms || 0);
    var chunks = s.chunks_in_prompt || 0;
    return (
      (ms / 1000).toFixed(1) +
      "s · " +
      chunks +
      (chunks === 1 ? " chunk" : " chunks") +
      " · " +
      formatCount(s.input_tokens || 0) +
      " in / " +
      formatCount(s.output_tokens || 0) +
      " out tokens"
    );
  }

  // Checks the shape of a query response. Returns a normalised copy, or null when it is not an answer.
  function normaliseAnswer(d) {
    if (!d || typeof d !== "object" || typeof d.answer !== "string") return null;
    var cites = Array.isArray(d.citations) ? d.citations : [];
    cites = cites.filter(function (c) {
      return c && typeof c === "object" && typeof c.n === "number" && typeof c.file === "string" && typeof c.snippet === "string";
    });
    return {
      answer: d.answer,
      grounded: d.grounded === true,
      refused: d.refused === true,
      truncated: d.truncated === true,
      citations: cites,
      stats: d.stats && typeof d.stats === "object" ? d.stats : {},
    };
  }

  // History: newest last, no consecutive duplicates, bounded.
  function pushHistory(list, entry, limit) {
    var e = String(entry == null ? "" : entry).trim();
    var out = list.slice();
    if (e === "" || (out.length > 0 && out[out.length - 1] === e)) return out;
    out.push(e);
    return out.length > limit ? out.slice(out.length - limit) : out;
  }

  // idx === len means 'the line being typed'. dir -1 is Up (older), +1 is Down (newer).
  function historyMove(len, idx, dir) {
    if (dir < 0) return idx > 0 ? idx - 1 : 0;
    return idx < len ? idx + 1 : len;
  }

  function findRepo(list, ref) {
    var r = String(ref == null ? "" : ref).trim();
    if (r === "") return null;
    var i;
    if (/^\d+$/.test(r)) {
      var id = parseInt(r, 10);
      for (i = 0; i < list.length; i++) {
        if (Number(list[i].id) === id) return list[i];
      }
      return null;
    }
    var low = r.toLowerCase();
    for (i = 0; i < list.length; i++) {
      if (repoLabel(list[i]).toLowerCase() === low) return list[i];
    }
    return null;
  }

  function toError(status, body) {
    var e = body && typeof body === "object" ? body.error : null;
    if (e && typeof e === "object" && typeof e.code === "string" && typeof e.message === "string") {
      return { code: e.code, message: e.message.slice(0, 500) };
    }
    return { code: "bad_response", message: "unexpected response from the API (HTTP " + status + ")" };
  }

  function networkError() {
    return { code: "network", message: "cannot reach the API" };
  }

  // First line is the error; further lines are hints.
  function errorLines(err) {
    var e = err || {};
    var code = typeof e.code === "string" ? e.code : "internal";
    var msg = typeof e.message === "string" ? e.message : "unknown error";
    var lines = ["error: " + code + ": " + msg];
    if (Object.prototype.hasOwnProperty.call(HINTS, code)) lines.push("hint: " + HINTS[code]);
    return lines;
  }

  function usageError(name) {
    var c = findCommand(name);
    return "error: usage: " + (c ? c.usage : name);
  }

  root.RPCore = {
    COMMANDS: COMMANDS,
    findCommand: findCommand,
    parseInput: parseInput,
    formatTable: formatTable,
    shortSha: shortSha,
    formatDate: formatDate,
    formatCount: formatCount,
    repoLabel: repoLabel,
    progressLine: progressLine,
    progressKey: progressKey,
    phaseText: phaseText,
    splitAnswer: splitAnswer,
    formatSnippet: formatSnippet,
    snippetHeader: snippetHeader,
    sourceLines: sourceLines,
    formatStats: formatStats,
    normaliseAnswer: normaliseAnswer,
    pushHistory: pushHistory,
    historyMove: historyMove,
    findRepo: findRepo,
    toError: toError,
    networkError: networkError,
    errorLines: errorLines,
    usageError: usageError,
  };
})(window);
