// Episteme 設計方向驗證用 mockup：導覽、時間帶、媒體庫與影音詳情的渲染。
(() => {
  const DATA = window.EPISTEME_MOCK;
  const h = (tag, attrs = {}, ...kids) => {
    const el = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs)) {
      if (v == null || v === false) continue;
      if (k === "class") el.className = v;
      else if (k === "style") Object.assign(el.style, v);
      else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
      else el.setAttribute(k, v === true ? "" : v);
    }
    for (const kid of kids.flat()) if (kid != null && kid !== false) el.append(kid.nodeType ? kid : document.createTextNode(kid));
    return el;
  };
  const icon = (name, fill = false) => h("i", { class: `${fill ? "ph-fill" : "ph"} ph-${name}`, "aria-hidden": "true" });
  const fmt = (t) => {
    t = Math.max(0, Math.round(t));
    const hh = Math.floor(t / 3600), mm = Math.floor((t % 3600) / 60), ss = t % 60;
    const p = (n) => String(n).padStart(2, "0");
    return hh ? `${hh}:${p(mm)}:${p(ss)}` : `${p(mm)}:${p(ss)}`;
  };
  const pct = (t, d) => `${(t / d) * 100}%`;

  // ---------- 主題 ----------
  const params = new URLSearchParams(location.search);
  const theme = params.get("theme") || localStorage.getItem("episteme-theme") || "light";
  document.documentElement.dataset.theme = theme;
  const toggleTheme = () => {
    const next = document.documentElement.dataset.theme === "dark" ? "light" : "dark";
    document.documentElement.dataset.theme = next;
    localStorage.setItem("episteme-theme", next);
    document.querySelectorAll("[data-theme-icon]").forEach((i) => (i.className = `ph ph-${next === "dark" ? "sun" : "moon"}`));
  };

  // ---------- 導覽 ----------
  const NAV = [
    [null, [["總覽", "squares-four", "#"], ["媒體庫", "books", "library.html"], ["訂閱", "rss", "#"], ["佇列", "stack", "#", 3]]],
    ["調整", [["Prompt", "chat-text", "#"], ["處理方案", "flow-arrow", "#"], ["字幕樣式", "subtitles", "#"], ["筆記模板", "note", "#"]]],
    ["系統", [["成本", "wallet", "#"], ["設定", "gear-six", "#"]]],
  ];
  function renderNav(current) {
    const nav = h("nav", { class: "nav", "aria-label": "主要導覽" },
      h("a", { class: "nav__mark cond", href: "library.html" }, "episteme"),
      h("button", { class: "button button--primary nav__new", type: "button" }, icon("plus"), h("span", {}, "新增")));
    for (const [group, items] of NAV) {
      if (group) nav.append(h("div", { class: "nav__group" }, group));
      for (const [label, ic, href, count] of items) {
        nav.append(h("a", {
          class: `nav__item${group ? " is-secondary" : ""}`, href,
          "aria-current": label === current ? "page" : null, title: label,
        }, icon(ic), h("span", { class: "nav__label" }, label), count ? h("span", { class: "nav__count num" }, String(count)) : null));
      }
    }
    nav.append(h("a", { class: "nav__item nav__attention", href: "#", title: "需要你處理" },
      icon("warning-circle"), h("span", { class: "nav__label" }, "需要你處理"), h("span", { class: "nav__count num" }, "2")));
    return nav;
  }
  function renderTopbar(title) {
    return h("header", { class: "topbar" },
      title ? h("h1", { class: "topbar__title cond" }, title) : null,
      h("div", { class: "topbar__spacer" }),
      h("div", { class: "command", role: "button", tabindex: "0" }, icon("magnifying-glass"), "搜尋或執行", h("kbd", {}, "Ctrl K")),
      h("button", { class: "icon-button", type: "button", "aria-label": "切換深淺色", onclick: toggleTheme },
        h("i", { class: `ph ph-${theme === "dark" ? "sun" : "moon"}`, "data-theme-icon": "", "aria-hidden": "true" })));
  }

  // ---------- 步驟帶的狀態 ----------
  function steps(item) {
    const d = item.duration, st = item.state;
    const list = [{ name: "字幕" }, { name: "校正" }];
    if (item.lang === "en") list.push({ name: "翻譯" });
    const whole = (kind) => [{ kind, from: 0, to: d }];
    const partial = (done, total) => [
      { kind: "done", from: 0, to: (d * done) / total },
      { kind: "developing", from: (d * done) / total, to: (d * (done + 1)) / total },
      { kind: "pending", from: (d * (done + 1)) / total, to: d },
    ];
    list.forEach((s) => (s.segs = whole("pending")));
    if (st.kind === "done") list.forEach((s) => (s.segs = whole("done")));
    if (st.kind === "transcribing") list[0].segs = partial(st.done, st.total);
    if (st.kind === "awaiting_agent") { list[0].segs = whole("done"); list[1].segs = whole("agent"); }
    if (st.kind === "correcting") { list[0].segs = whole("done"); list[1].segs = partial(st.done, st.total); }
    if (st.kind === "failed") {
      list[0].segs = whole("done");
      const a = (d * (st.chunk - 1)) / st.total, b = (d * st.chunk) / st.total;
      list[1].segs = [{ kind: "done", from: 0, to: a }, { kind: "failed", from: a, to: b }, { kind: "done", from: b, to: d }];
    }
    return list;
  }
  function summary(item) {
    const st = item.state;
    switch (st.kind) {
      case "done": return "全部完成";
      case "transcribing": return `字幕轉錄中，第 ${st.done + 1}/${st.total} 段`;
      case "awaiting_agent": return "字幕完成，校正等待 Agent";
      case "correcting": return `字幕完成，校正中，第 ${st.done + 1}/${st.total} 段`;
      case "failed": return `字幕完成，校正第 ${st.chunk} 段失敗`;
    }
  }
  function status(item) {
    const st = item.state;
    const line = (cls, ic, fill, text, small) => h("div", { class: `status status--${cls}` }, icon(ic, fill), h("div", {}, text, small ? h("small", {}, small) : null));
    switch (st.kind) {
      case "done": return line("done", "check-circle", true, "完成", item.lang === "en" ? "含 zh-Hant-TW 譯文" : `${item.frames.length} 張畫面，筆記已寫入`);
      case "transcribing": return line("running", "circle-half", false, `轉錄 ${st.done + 1}/${st.total} 段`, st.source);
      case "awaiting_agent": return line("agent", "robot", false, "校正等待 Agent", "也可以改用 API 處理");
      case "correcting": return line("running", "circle-half", false, `校正 ${st.done + 1}/${st.total} 段`, "API 處理中");
      case "failed": return line("failed", "x-circle", false, `第 ${st.chunk} 段校正失敗`, "驗證失敗 3 次");
    }
  }
  const showFrames = (item) => item.state.kind === "done" || item.id === DATA.detail.id;

  // ---------- 時間帶 ----------
  function ribbon(item, { full = false } = {}) {
    const d = item.duration;
    const root = h("div", { class: `ribbon${full ? " ribbon--full" : ""}` });
    if (full) {
      const scale = h("div", { class: "ribbon__scale num", "aria-hidden": "true" });
      for (let t = 0; t < d - 60; t += 120) scale.append(h("span", { style: { left: pct(t, d) } }, fmt(t)));
      scale.append(h("span", { style: { left: "100%" } }, fmt(d)));
      root.append(scale);
    }
    const marks = h("div", { class: "ribbon__marks", "aria-hidden": "true" });
    if (showFrames(item)) {
      for (const t of item.frames) {
        marks.append(h("span", { class: "ribbon__frame", style: { left: pct(t, d) } }, svgTriangle()));
      }
    }
    root.append(marks);

    const track = h("div", { class: "ribbon__track" });
    const known = item.state.kind === "transcribing" ? item.state.done / item.state.total : 1;
    const density = densitySvg(item.density, known);
    track.append(density);
    const chapters = full ? item.chapters : item.chapters.map((t) => ({ t, lv: 2 }));
    for (const c of chapters) {
      if (c.t <= 0) continue;
      track.append(h("span", { class: `ribbon__chapter${c.lv === 3 ? " ribbon__chapter--minor" : ""}`, style: { left: pct(c.t, d) } }));
    }
    if (full) {
      // 在底軌上標出正在處理或失敗的時間範圍，對應下方的步驟帶
      for (const st of steps(item)) for (const seg of st.segs) {
        if (seg.kind === "developing" || seg.kind === "failed") {
          track.append(h("span", { class: `ribbon__range ribbon__range--${seg.kind}`, style: { left: pct(seg.from, d), width: pct(seg.to - seg.from, d) } }));
        }
      }
    }
    root.append(track);

    if (full) {
      const labels = h("div", { class: "ribbon__labels", "aria-hidden": "true" });
      const majors = item.chapters.filter((c) => c.lv === 2);
      majors.forEach((c, i) => {
        const end = majors[i + 1] ? majors[i + 1].t : d;
        labels.append(h("span", { style: { left: pct(c.t, d), width: `calc(${pct(end - c.t, d)} - 6px)` }, title: c.title }, c.title));
      });
      root.append(labels);
    }

    const bands = h("div", { class: "ribbon__bands" });
    const stepList = steps(item);
    for (const s of stepList) {
      const band = h("div", { class: "band" });
      for (const seg of s.segs) {
        band.append(h("span", { class: `seg seg--${seg.kind}`, style: { left: pct(seg.from, d), width: pct(seg.to - seg.from, d) } }));
      }
      const active = s.segs.some((x) => ["developing", "failed", "agent"].includes(x.kind));
      bands.append(h("div", { class: "band-row" }, band, full ? h("span", { class: `band__label${active ? " is-active" : ""}` }, bandLabel(item, s)) : null));
    }
    root.append(bands);
    root.append(h("div", { class: "ribbon__overlay" }));
    root.setAttribute("role", "img");
    root.setAttribute("aria-label", summary(item));
    root._steps = stepList;
    return root;
  }
  function bandLabel(item, s) {
    const st = item.state;
    if (s.segs.some((x) => x.kind === "developing")) return `${s.name} ${st.done + 1}/${st.total}`;
    if (s.segs.some((x) => x.kind === "failed")) return `${s.name}：第 ${st.chunk} 段失敗`;
    if (s.segs.some((x) => x.kind === "agent")) return `${s.name}：等待 Agent`;
    if (s.segs.every((x) => x.kind === "done")) return `${s.name}：完成`;
    return s.name;
  }
  function densitySvg(values, known) {
    const ns = "http://www.w3.org/2000/svg";
    const n = values.length;
    const sm = values.map((v, i) => (values[Math.max(i - 1, 0)] + 2 * v + values[Math.min(i + 1, n - 1)]) / 4);
    const k = Math.max(1, Math.round(n * known));
    const pts = sm.slice(0, k).map((v, i) => [i + 0.5, 100 - Math.max(v, 0.03) * 94]);
    const line = `M0,${pts[0][1]} ` + pts.map(([x, y]) => `L${x},${y}`).join(" ") + ` L${k},${pts[k - 1][1]}`;
    const svg = document.createElementNS(ns, "svg");
    svg.setAttribute("class", "ribbon__density");
    svg.setAttribute("viewBox", `0 0 ${n} 100`);
    svg.setAttribute("preserveAspectRatio", "none");
    svg.setAttribute("aria-hidden", "true");
    const mk = (cls, d) => { const p = document.createElementNS(ns, "path"); p.setAttribute("class", cls); p.setAttribute("d", d); return p; };
    svg.append(mk("area", `M0,100 L${line.slice(1)} L${k},100 Z`), mk("line", line));
    if (k < n) svg.append(mk("unknown", `M${k},99 L${n},99`));
    return svg;
  }
  function svgTriangle() {
    const ns = "http://www.w3.org/2000/svg";
    const svg = document.createElementNS(ns, "svg");
    svg.setAttribute("viewBox", "0 0 8 7");
    const p = document.createElementNS(ns, "polygon");
    p.setAttribute("points", "0.5,0.5 7.5,0.5 4,6.5");
    svg.append(p);
    return svg;
  }

  // ---------- 媒體庫 ----------
  function renderLibrary(mount) {
    const items = [...DATA.items];
    const groups = new Map();
    items.sort((a, b) => b.published.localeCompare(a.published));
    for (const it of items) {
      if (!groups.has(it.channel)) groups.set(it.channel, []);
      groups.get(it.channel).push(it);
    }
    mount.append(
      h("div", { class: "toolbar" },
        h("label", { class: "search" }, icon("magnifying-glass"), h("span", { class: "visually-hidden" }, "搜尋"),
          h("input", { type: "search", placeholder: "搜尋標題或字幕內容" })),
        ...["頻道", "平台", "狀態", "發布日", "更多"].map((f) => h("button", { class: "filter filter--facet", type: "button" }, f, icon("caret-down"))),
        h("button", { class: "filter filter--all", type: "button" }, icon("funnel"), "篩選"),
        h("div", { class: "topbar__spacer" }),
        h("button", { class: "filter", type: "button" }, "依發布日排序", icon("caret-down")),
        h("div", { class: "segmented", role: "group", "aria-label": "檢視方式" },
          h("button", { type: "button", "aria-pressed": "true", title: "列" }, icon("rows")),
          h("button", { type: "button", "aria-pressed": "false", title: "格" }, icon("grid-four")))),
      h("div", { class: "applied" },
        h("span", { class: "chip" }, "平台是 YouTube", h("button", { type: "button", "aria-label": "移除條件" }, icon("x"))),
        h("button", { class: "button button--text", type: "button" }, "清除"),
        h("button", { class: "button button--text", type: "button" }, "存成檢視")),
      h("button", { class: "library__notice", type: "button" }, icon("arrow-up"), "有 1 個新項目"));

    for (const [channel, list] of groups) {
      const ul = h("ul", { class: "rows" });
      for (const it of list) {
        const href = it.id === DATA.detail.id ? "item.html" : "#";
        ul.append(h("li", {}, h("a", { class: "row", href },
          h("img", { class: "thumb", alt: "", loading: "lazy", src: `https://i.ytimg.com/vi/${it.youtubeId}/mqdefault.jpg` }),
          h("div", {},
            h("p", { class: "row__title", lang: it.lang }, it.title),
            h("div", { class: "row__meta" }, icon("youtube-logo"), h("span", { class: "visually-hidden" }, "YouTube"), h("span", { class: "num" }, `${it.published} 發布`), h("span", { class: "row__meta-duration num" }, `長度 ${fmt(it.duration)}`))),
          h("div", { class: "row__duration num", "aria-label": `長度 ${fmt(it.duration)}` }, fmt(it.duration)),
          ribbon(it),
          status(it))));
      }
      mount.append(h("section", { class: "group" },
        h("div", { class: "group__head" }, h("h2", { class: "group__title cond" }, channel), h("span", { class: "group__count num" }, `${list.length} 部`)),
        ul));
    }
  }

  // ---------- 影音詳情 ----------
  function renderItem(mount) {
    const it = DATA.detail;
    const d = it.duration;
    const st = it.state;
    const runFrom = (d * st.done) / st.total, runTo = (d * (st.done + 1)) / st.total;
    let playhead = it.playhead;

    const rib = ribbon(it, { full: true });
    rib.setAttribute("role", "group");
    rib.setAttribute("aria-label", `時間帶：${summary(it)}`);
    const track = rib.querySelector(".ribbon__track");
    track.setAttribute("role", "slider");
    track.setAttribute("tabindex", "0");
    track.setAttribute("aria-label", "播放位置");
    track.setAttribute("aria-valuemin", "0");
    track.setAttribute("aria-valuemax", String(Math.round(d)));
    const overlay = rib.querySelector(".ribbon__overlay");
    const head = h("span", { class: "ribbon__playhead" });
    const tip = h("div", { class: "ribbon__tooltip", role: "presentation" });
    overlay.append(head, tip);

    const chapterAt = (t) => [...it.chapters].reverse().find((c) => c.t <= t) || it.chapters[0];
    const majorAt = (t) => [...it.chapters].reverse().find((c) => c.lv === 2 && c.t <= t) || it.chapters[0];
    const cueAt = (t) => it.cues.find((c) => c.s <= t && t < c.e + 0.3);

    const timeLabel = h("span", { class: "num" });
    const chapterList = h("ol", { class: "chapters" });
    for (const c of it.chapters) {
      chapterList.append(h("li", { class: c.lv === 3 ? "is-minor" : null, "data-t": c.t },
        h("a", { href: "#", onclick: (e) => { e.preventDefault(); seek(c.t); } }, h("span", {}, c.title), h("span", { class: "num" }, fmt(c.t)))));
    }
    const cueList = h("ol", { class: "cues", lang: it.lang });
    for (const c of it.cues) {
      cueList.append(h("li", { class: "cue", "data-s": c.s },
        h("time", { class: "num", onclick: () => seek(c.s) }, fmt(c.s)), h("span", {}, c.t)));
    }

    function seek(t, { scroll = true } = {}) {
      playhead = Math.min(Math.max(t, 0), d);
      head.style.left = pct(playhead, d);
      track.setAttribute("aria-valuenow", String(Math.round(playhead)));
      track.setAttribute("aria-valuetext", fmt(playhead));
      timeLabel.textContent = `${fmt(playhead)} / ${fmt(d)}`;
      const cur = chapterAt(playhead);
      chapterList.querySelectorAll("li").forEach((li) => li.classList.toggle("is-current", Number(li.dataset.t) === cur.t));
      const cue = cueAt(playhead);
      cueList.querySelectorAll(".cue").forEach((li) => {
        const on = cue && Number(li.dataset.s) === cue.s;
        li.classList.toggle("is-current", on);
        if (on && scroll) cueList.scrollTop = li.offsetTop - cueList.offsetTop - cueList.clientHeight / 3;
      });
    }
    const timeFromEvent = (e) => {
      const r = track.getBoundingClientRect();
      return (Math.min(Math.max(e.clientX - r.left, 0), r.width) / r.width) * d;
    };
    track.addEventListener("mousemove", (e) => {
      const t = timeFromEvent(e), cue = cueAt(t);
      tip.replaceChildren(...[h("div", { class: "num" }, fmt(t)), h("div", { class: "secondary" }, chapterAt(t).title), cue ? h("div", { lang: it.lang }, cue.t) : null].filter(Boolean));
      tip.style.left = `${e.clientX - overlay.getBoundingClientRect().left}px`;
      tip.classList.add("is-visible");
    });
    track.addEventListener("mouseleave", () => tip.classList.remove("is-visible"));
    track.addEventListener("click", (e) => seek(timeFromEvent(e)));
    track.addEventListener("keydown", (e) => {
      if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
      e.preventDefault();
      const dir = e.key === "ArrowRight" ? 1 : -1;
      if (e.shiftKey) {
        const majors = it.chapters.filter((c) => c.lv === 2);
        const next = dir > 0 ? majors.find((c) => c.t > playhead + 0.5) : [...majors].reverse().find((c) => c.t < playhead - 0.5);
        seek(next ? next.t : dir > 0 ? d : 0);
      } else seek(playhead + dir * 5);
    });

    mount.append(
      h("a", { class: "crumb", href: "library.html" }, "媒體庫", icon("caret-right")),
      h("div", { class: "item-head" },
        h("h1", { class: "item-head__title cond", lang: it.lang }, it.title),
        h("div", { class: "item-head__actions" },
          h("button", { class: "button button--outline", type: "button" }, "重新處理", icon("caret-down")),
          h("button", { class: "button button--primary", type: "button" }, "匯出", icon("caret-down")))),
      h("dl", { class: "facts" },
        ...[["頻道", it.channelFull], ["平台", "YouTube"], ["發布", it.published], ["長度", fmt(d)], ["處理方案", "標準筆記"], ["字幕來源", "YouTube 人工字幕"]]
          .map(([k, v]) => h("div", {}, h("dt", {}, k), h("dd", { class: k === "長度" || k === "發布" ? "num" : null }, v)))),
      h("div", { class: "sheet" }, rib),
      h("div", { class: "detail-grid" },
        h("div", {},
          h("div", { class: "player" },
            h("img", { alt: "影片畫面", src: `https://i.ytimg.com/vi/${it.youtubeId}/maxresdefault.jpg`, onerror: (e) => (e.target.src = `https://i.ytimg.com/vi/${it.youtubeId}/hqdefault.jpg`) }),
            h("div", { class: "player__bar" }, icon("play", true), timeLabel)),
          chapterList),
        h("section", { class: "sheet", "aria-label": "字幕" },
          h("div", { class: "tabs", role: "tablist" },
            h("button", { role: "tab", "aria-selected": "true" }, "字幕", h("span", { class: "num secondary" }, String(it.cueCount))),
            h("button", { role: "tab", "aria-selected": "false" }, "畫面", h("span", { class: "num secondary" }, String(it.frames.length))),
            h("button", { role: "tab", "aria-selected": "false" }, "筆記"),
            h("button", { role: "tab", "aria-selected": "false" }, "紀錄")),
          h("p", { class: "progress-note" }, icon("circle-half"),
            h("span", {}, `校正處理中：第 ${st.done + 1} 段（${fmt(runFrom)}–${fmt(runTo)}），前 ${st.done} 段已可查看。`)),
          h("div", { class: "cue-tools" },
            h("div", { class: "segmented", role: "group", "aria-label": "顯示模式" },
              h("button", { type: "button", "aria-pressed": "true" }, "原始"),
              h("button", { type: "button", "aria-pressed": "false" }, "校正"),
              h("button", { type: "button", "aria-pressed": "false" }, "差異")),
            h("span", { class: "secondary" }, "原文 zh-Hans，目標 zh-Hant-TW")),
          cueList)));
    seek(playhead, { scroll: false });
    requestAnimationFrame(() => seek(playhead));
  }

  window.EpistemeMockup = { renderNav, renderTopbar, renderLibrary, renderItem };
})();
