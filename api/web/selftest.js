// In-browser self-test for core.js and safe rendering. Loaded only when the page URL ends in #selftest.
(function () {
  "use strict";

  var C = window.RPCore;
  var UI = window.RPUI;
  var passed = 0;
  var failures = [];

  function eq(name, got, want) {
    var g = JSON.stringify(got);
    var w = JSON.stringify(want);
    if (g === w) {
      passed++;
    } else {
      failures.push(name + "\n    got:  " + g + "\n    want: " + w);
    }
  }

  // ---- input parsing ----
  eq("parse empty", C.parseInput("").kind, "empty");
  eq("parse spaces only", C.parseInput("   ").kind, "empty");
  eq("parse command", C.parseInput("repos"), { kind: "command", name: "repos", args: [], rest: "" });
  eq("parse command with extra spaces", C.parseInput("  use    1  "), { kind: "command", name: "use", args: ["1"], rest: "1" });
  eq("parse add url", C.parseInput("add https://github.com/a/b").args, ["https://github.com/a/b"]);
  eq("parse bare text is a question", C.parseInput("where is main?"), { kind: "ask", question: "where is main?" });
  eq("parse ask prefix", C.parseInput("ask where is main?"), { kind: "ask", question: "where is main?" });
  eq("parse ask with no question", C.parseInput("ask"), { kind: "ask", question: "" });
  eq("parse question starting with a command word after ask", C.parseInput("ask show me the server"), {
    kind: "ask",
    question: "show me the server",
  });
  eq("parse capitalised command word is a question", C.parseInput("Help me").kind, "ask");
  eq("parse command word inside a question", C.parseInput("how do I use this").kind, "ask");
  eq("parse keeps inner spacing of a question", C.parseInput("a  b").question, "a  b");

  // ---- tables and formats ----
  eq("table aligns columns", C.formatTable([["id", "status"], ["10", "ready"]]), ["id  status", "10  ready"]);
  eq("table does not pad the last column", C.formatTable([["a", "b"], ["ccc", "d"]]), ["a    b", "ccc  d"]);
  eq("short sha", C.shortSha("7f05d217867b2af52b0a28c6d1c91df97e1b5b39"), "7f05d21");
  eq("short sha missing", C.shortSha(null), "-");
  eq("date", C.formatDate("2026-09-27T10:11:12.5Z"), "2026-09-27");
  eq("date bad", C.formatDate("yesterday"), "-");
  eq("count", C.formatCount(1293), "1,293");
  eq("count small", C.formatCount(52), "52");
  eq("count large", C.formatCount(1234567), "1,234,567");

  // ---- progress ----
  var repo = { owner: "golang", name: "example", commit_sha: "7f05d217867b", status: "indexing" };
  function det(status, phase, done, total) {
    return Object.assign({}, repo, { status: status, progress: { phase: phase, files_done: done, files_total: total } });
  }
  eq("progress queued", C.progressLine(det("queued", null, null, null)), "[queued]");
  eq("progress cloning", C.progressLine(det("indexing", "cloning", null, null)), "[cloning]");
  eq("progress scanning", C.progressLine(det("indexing", "scanning", 0, 43)), "[scanning] 43 files");
  eq("progress chunking counts files", C.progressLine(det("indexing", "chunking", 20, 43)), "[chunking] 20/43 files");
  eq("progress embedding counts chunks", C.progressLine(det("indexing", "embedding", 96, 248)), "[embedding] 96/248 chunks");
  eq("progress storing", C.progressLine(det("indexing", "storing", null, null)), "[storing]");
  eq("progress indexing without phase", C.progressLine(det("indexing", null, null, null)), "[indexing]");
  eq("progress ready", C.progressLine(det("ready", null, null, null)), "[ready] golang/example @ 7f05d21");
  eq("progress failed", C.progressLine(Object.assign(det("failed", null, null, null), { error: "boom" })), "[failed] boom");
  eq("phase text", C.phaseText(det("indexing", "embedding", 96, 248)), "embedding 96/248 chunks");
  eq("phase text empty", C.phaseText(det("ready", null, null, null)), "");
  eq(
    "key is stable inside a 10% bucket",
    C.progressKey(det("indexing", "embedding", 96, 248)) === C.progressKey(det("indexing", "embedding", 98, 248)),
    true
  );
  eq(
    "key changes across buckets",
    C.progressKey(det("indexing", "embedding", 96, 248)) === C.progressKey(det("indexing", "embedding", 130, 248)),
    false
  );
  eq(
    "key changes with the phase",
    C.progressKey(det("indexing", "chunking", 0, 43)) === C.progressKey(det("indexing", "embedding", 0, 43)),
    false
  );
  eq("key handles total 0", C.progressKey(det("indexing", "embedding", 0, 0)), "indexing|embedding|0");
  eq("key clamps done above total", C.progressKey(det("indexing", "chunking", 50, 43)), "indexing|chunking|10");
  eq("key scanning uses the file total", C.progressKey(det("indexing", "scanning", 0, 43)), "indexing|scanning|43");

  // ---- answer segments ----
  function segs(text, ns) {
    return C.splitAnswer(text, ns).map(function (s) {
      return s.type === "cite" ? "#" + s.n : s.text;
    });
  }
  eq("split valid marker", segs("It polls [6] here.", [6]), ["It polls ", "#6", " here."]);
  eq("split unknown number stays text", segs("It polls [9] here.", [6]), ["It polls [9] here."]);
  eq("split adjacent markers", segs("Both [7][8].", [7, 8]), ["Both ", "#7", "#8", "."]);
  eq("split marker at start", segs("[1] first", [1]), ["#1", " first"]);
  eq("split marker at end", segs("last [1]", [1]), ["last ", "#1"]);
  eq("split index after an identifier is text", segs("use items[1] here", [1]), ["use items[1] here"]);
  eq("split doubled index is text", segs("grid[1][2]", [1, 2]), ["grid[1][2]"]);
  eq("split marker after punctuation", segs("(see [2]).", [2]), ["(see ", "#2", ")."]);
  eq("split marker after a comma", segs("a,[3]", [3]), ["a,", "#3"]);
  eq("split non-numeric brackets are text", segs("[abc] and [] and [1a]", [1]), ["[abc] and [] and [1a]"]);
  eq("split unicode text", segs("日本語 [1] です", [1]), ["日本語 ", "#1", " です"]);
  eq("split empty", segs("", [1]), []);
  eq("split no citations", segs("plain [1]", []), ["plain [1]"]);
  eq("split keeps every character", segs("a [1] b [2] c", [1, 2]).join(""), "a [1] b [2] c".replace(/\[(\d)\]/g, "#$1"));

  // ---- snippets ----
  eq("snippet numbers", C.formatSnippet("a\nb", 65), ["65 | a", "66 | b"]);
  eq("snippet width 99 to 100", C.formatSnippet("a\nb", 99), [" 99 | a", "100 | b"]);
  eq("snippet width 999 to 1000", C.formatSnippet("a\nb", 999), [" 999 | a", "1000 | b"]);
  eq("snippet empty line", C.formatSnippet("a\n\nb", 1), ["1 | a", "2 | ", "3 | b"]);
  eq("snippet keeps tabs", C.formatSnippet("\tx", 1), ["1 | \tx"]);
  eq("snippet CRLF", C.formatSnippet("a\r\nb", 1), ["1 | a", "2 | b"]);
  eq("snippet trailing newline", C.formatSnippet("a\nb\n", 1), ["1 | a", "2 | b"]);
  eq("snippet without trailing newline", C.formatSnippet("a", 7), ["7 | a"]);
  var cite = { n: 2, file: "outyet/main.go", start_line: 65, end_line: 80, symbol: "isTagged", kind: "function", language: "go", snippet: "x" };
  eq("snippet header", C.snippetHeader(cite), "[2] outyet/main.go:65-80 (go, function isTagged)");
  eq("snippet header without symbol", C.snippetHeader({ n: 1, file: "a.md", start_line: 1, end_line: 2, kind: "doc", language: "markdown" }), "[1] a.md:1-2 (markdown, doc)");
  eq("source lines", C.sourceLines([cite, { n: 10, file: "a.go", start_line: 1, end_line: 2, symbol: "" }]), [
    "  [2]   outyet/main.go:65-80  isTagged",
    "  [10]  a.go:1-2",
  ]);
  eq("stats", C.formatStats({ embed_ms: 753, search_ms: 10, llm_ms: 1115, chunks_in_prompt: 8, input_tokens: 1293, output_tokens: 52 }), "1.9s · 8 chunks · 1,293 in / 52 out tokens");
  eq("stats singular", C.formatStats({ chunks_in_prompt: 1 }).indexOf("1 chunk ·"), 7);
  eq(
    "stats with mode and rerank",
    C.formatStats({ retrieval_mode: "hybrid_rerank", embed_ms: 300, search_ms: 1800, llm_ms: 1500, rerank_ms: 1690, chunks_in_prompt: 8 }),
    "3.6s · hybrid_rerank (rerank 1.7s) · 8 chunks · 0 in / 0 out tokens"
  );
  eq("stats rerank cached", C.formatStats({ retrieval_mode: "hybrid_rerank", rerank_ms: 1690, rerank_cached: true }).indexOf("(rerank cached)") > 0, true);
  eq("stats rerank failed", C.formatStats({ retrieval_mode: "hybrid_rerank", rerank_fallback: "timeout", rerank_ms: 10000 }).indexOf("(rerank failed)") > 0, true);
  eq("stats vector mode", C.formatStats({ retrieval_mode: "vector" }), "0.0s · vector · 0 chunks · 0 in / 0 out tokens");
  eq("rerank note", C.rerankNote({ rerank_fallback: "timeout" }), "rerank failed (timeout); hybrid order used");
  eq("rerank note none", C.rerankNote({ rerank_cached: true }), null);

  // ---- ask options ----
  var P = C.parseAskOptions;
  eq("ask no options", P("where is main?"), { question: "where is main?", filters: null, error: null });
  eq("ask lang", P("--lang go where is main?"), { question: "where is main?", filters: { language: ["go"] }, error: null });
  eq("ask lang list and path", P("--lang go,python --path api/ q"), {
    question: "q",
    filters: { language: ["go", "python"], path_prefix: "api/" },
    error: null,
  });
  eq("ask equals form", P("--path=src/ --lang=ts,go q").filters, { language: ["ts", "go"], path_prefix: "src/" });
  eq("ask repeated lang merges", P("--lang go --lang go,python q").filters.language, ["go", "python"]);
  eq("ask double dash ends options", P("--lang go -- --path is a word here"), {
    question: "--path is a word here",
    filters: { language: ["go"] },
    error: null,
  });
  eq("ask options only inside the text stay text", P("what does --lang do?").question, "what does --lang do?");
  eq("ask unknown option", P("--language go q").error, "unknown option --language");
  eq("ask missing value", P("--lang").error, "--lang needs a value");
  eq("ask value cannot be an option", P("--lang --path x q").error, "--lang needs a value");
  eq("ask empty equals value", P("--path= q").error, "--path needs a value");
  eq("ask path twice", P("--path a --path b q").error, "--path given twice");
  eq("ask options without question", P("--lang go").question, "");
  eq("filter line", C.filterLine({ language: ["go", "python"], path_prefix: "api/" }), "filters: language go, python · path api/");
  eq("filter line none", C.filterLine(null), null);
  eq("bare text with options is a question", C.parseInput("--lang go q").kind, "ask");

  // ---- response shape ----
  eq("normalise rejects non-objects", C.normaliseAnswer("x"), null);
  eq("normalise rejects a missing answer", C.normaliseAnswer({ citations: [] }), null);
  eq("normalise fills defaults", C.normaliseAnswer({ answer: "a" }).citations, []);
  eq("normalise drops malformed citations", C.normaliseAnswer({ answer: "a", citations: [cite, { n: "x" }, null] }).citations.length, 1);
  eq("normalise flags are strict booleans", C.normaliseAnswer({ answer: "a", grounded: "yes" }).grounded, false);

  // ---- history ----
  eq("history push", C.pushHistory(["a"], "b", 5), ["a", "b"]);
  eq("history ignores empty", C.pushHistory(["a"], "  ", 5), ["a"]);
  eq("history ignores a consecutive duplicate", C.pushHistory(["a"], "a", 5), ["a"]);
  eq("history keeps a later duplicate", C.pushHistory(["a", "b"], "a", 5), ["a", "b", "a"]);
  eq("history limit", C.pushHistory(["a", "b", "c"], "d", 3), ["b", "c", "d"]);
  eq("history does not change its input", (function () {
    var l = ["a"];
    C.pushHistory(l, "b", 5);
    return l;
  })(), ["a"]);
  eq("history up from the draft", C.historyMove(3, 3, -1), 2);
  eq("history up stops at the oldest", C.historyMove(3, 0, -1), 0);
  eq("history down returns to the draft", C.historyMove(3, 2, 1), 3);
  eq("history down stays at the draft", C.historyMove(3, 3, 1), 3);

  // ---- repository lookup ----
  var list = [
    { id: 1, owner: "golang", name: "example", status: "ready" },
    { id: 12, owner: "Foo", name: "Bar", status: "queued" },
  ];
  eq("find by id", C.findRepo(list, "12").name, "Bar");
  eq("find by owner/name in any case", C.findRepo(list, "FOO/bar").id, 12);
  eq("find unknown id", C.findRepo(list, "2"), null);
  eq("find unknown name", C.findRepo(list, "x/y"), null);
  eq("find empty", C.findRepo(list, " "), null);
  eq("find by name only does not match", C.findRepo(list, "example"), null);

  // ---- errors ----
  eq("error from the API shape", C.toError(400, { error: { code: "invalid_url", message: "bad" } }), { code: "invalid_url", message: "bad" });
  eq("error from an unexpected body", C.toError(502, "<html>").code, "bad_response");
  eq("error from a null body", C.toError(500, null).message, "unexpected response from the API (HTTP 500)");
  eq("error message is capped", C.toError(400, { error: { code: "x", message: new Array(2001).join("a") } }).message.length, 500);
  eq("network error", C.networkError().code, "network");
  ["invalid_url", "invalid_request", "not_found", "internal"].forEach(function (code) {
    eq("error line for " + code, C.errorLines({ code: code, message: "m" }), ["error: " + code + ": m"]);
  });
  ["repo_not_ready", "rate_limited", "not_configured", "network", "timeout", "upstream_error", "model_mismatch", "answer_blocked"].forEach(function (code) {
    var lines = C.errorLines({ code: code, message: "m" });
    eq("error line and hint for " + code, [lines.length, lines[0], lines[1].indexOf("hint: ")], [2, "error: " + code + ": m", 0]);
  });
  eq("error lines for a malformed error", C.errorLines(null), ["error: internal: unknown error"]);
  eq("usage error", C.usageError("show"), "error: usage: show <n>");

  // ---- syntax highlighting ----
  function toks(text, lang) {
    return C.tokenize(text, lang)
      .filter(function (t) {
        return t.t !== "";
      })
      .map(function (t) {
        return t.t + ":" + t.s;
      });
  }
  var pySrc = '@cached\ndef render(self, n=10):\n    # draw it\n    if not self.hidden:\n        return echo(f"x{n}", 0x1F)\n';
  eq("tokens give back every character", C.tokenize(pySrc, "python").map(function (t) {
    return t.s;
  }).join(""), pySrc);
  eq("python tokens", toks(pySrc, "python"), [
    "dec:@cached", "kw:def", "fn:render", "const:self", "num:10", "com:# draw it", "ctl:if", "kw:not", "const:self",
    "ctl:return", "fn:echo", 'str:f"x{n}"', "num:0x1F",
  ]);
  eq("python triple-quoted string spans lines", toks('x = """a\nb"""', "python"), ['str:"""a\nb"""']);
  eq("go tokens", toks("func (c *Cmd) Run(args []string) error {\n\treturn nil // done\n}", "go"), [
    "kw:func", "type:Cmd", "fn:Run", "type:string", "type:error", "ctl:return", "const:nil", "com:// done",
  ]);
  eq("go raw string", toks("s := `a\\b`", "go"), ["str:`a\\b`"]);
  eq("js tokens", toks("export class A extends B { get() { return this.x.y(`t${1}`) } }", "typescript"), [
    "kw:export", "kw:class", "type:A", "kw:extends", "type:B", "fn:get", "ctl:return", "const:this", "fn:y", "str:`t${1}`",
  ]);
  eq("property names after a dot are plain", toks("obj.match + obj.if", "javascript"), []);
  eq("block comment", toks("a /* x\ny */ b", "go"), ["com:/* x\ny */"]);
  eq("unknown language is plain", C.tokenize("# Title", "markdown"), [{ t: "", s: "# Title" }]);
  eq("empty snippet has no tokens", C.tokenize("", "python"), []);
  eq("highlight splits lines", C.highlightSnippet('a = "x"\r\n\nb\n', "python").map(function (l) {
    return l.map(function (t) {
      return t.s;
    }).join("");
  }), ['a = "x"', "", "b"]);
  eq("gutters match formatSnippet", C.snippetGutters(99, 2), [" 99 | ", "100 | "]);

  // ---- rendering safety ----
  var payloads = ["<script>alert(1)</script>", "<img src=x onerror=alert(1)>", "&lt;b&gt;bold&lt;/b&gt;", "</pre><a href=\"x\">link</a>"];
  payloads.forEach(function (p, i) {
    var box = document.createElement("div");
    UI.renderSnippet(box, C.formatSnippet("x := 1\n" + p + "\ny := 2", 1));
    eq("snippet payload " + i + " creates only the pre", box.querySelectorAll("*").length, 1);
    eq("snippet payload " + i + " appears as text", box.textContent.indexOf(p) !== -1, true);

    ["python", "go", "typescript", "markdown"].forEach(function (lang) {
      var code = document.createElement("div");
      UI.renderCode(code, "x = 1\n" + p + '\ns = "' + p + '" # ' + p, 1, lang);
      var tags = Array.prototype.map.call(code.querySelectorAll("*"), function (e) {
        return e.tagName + (e.attributes.length === 1 && e.hasAttribute("class") ? "" : "!attrs");
      });
      eq("code payload " + i + " " + lang + " creates only the pre and spans", tags.filter(function (t) {
        return t !== "PRE" && t !== "SPAN";
      }), []);
      eq("code payload " + i + " " + lang + " appears as text", code.textContent.indexOf(p) !== -1, true);
    });

    var ans = document.createElement("div");
    UI.renderAnswer(ans, C.splitAnswer("See " + p + " and [1] done", [1]), function () {});
    eq("answer payload " + i + " creates only the citation button", ans.querySelectorAll("*").length, 1);
    eq("answer payload " + i + " appears as text", ans.textContent.indexOf(p) !== -1, true);
  });
  (function () {
    var ans = document.createElement("div");
    var clicked = [];
    UI.renderAnswer(ans, C.splitAnswer("a [1] b [2]", [1, 2]), function (n) {
      clicked.push(n);
    });
    var buttons = ans.querySelectorAll("button");
    eq("citation buttons are rendered", buttons.length, 2);
    buttons[1].click();
    eq("citation button runs its number", clicked, [2]);
    eq("citation button has an accessible name", buttons[0].getAttribute("aria-label"), "show citation 1");
  })();

  // ---- report ----
  UI.print("selftest: " + passed + " passed, " + failures.length + " failed", failures.length === 0 ? "ok" : "err");
  failures.forEach(function (f) {
    UI.print("FAIL " + f, "err");
  });
})();
