import { defaultKeymap } from "@codemirror/commands";
import { Compartment, EditorState } from "@codemirror/state";
import {
  EditorView,
  ViewPlugin,
  ViewUpdate,
  keymap,
  lineNumbers,
} from "@codemirror/view";
import { useCallback, useEffect, useRef, useState } from "react";
import * as Y from "yjs";
import { yCollab } from "y-codemirror.next";
import { renderGFM } from "./md/render";
import { DiagramPreview } from "./DiagramPreview";

export type SpaceInfo = {
  key: string;
  display_name: string;
  repo_url: string;
  branch: string;
};

type Props = {
  spaces: SpaceInfo[];
  space: string;
  onSpaceChange: (key: string) => void;
  path: string;
  onPathChange: (p: string) => void;
  userName: string;
  onLogout: () => void;
};

const DEFAULT_PAGE_TEXT = `# Welcome

Edit this page on the left. The right side shows preview (GitHub Flavored Markdown).`;

/** Seeded from HTTP on the leader tab only — never broadcast as a live edit. */
const ORIGIN_INIT = "init";

function uint8ToBase64(u8: Uint8Array): string {
  let binary = "";
  for (let i = 0; i < u8.length; i++) {
    binary += String.fromCharCode(u8[i]!);
  }
  return btoa(binary);
}

export default function WikiEditor({
  spaces,
  space,
  onSpaceChange,
  path,
  onPathChange,
  userName,
  onLogout,
}: Props) {
  const host = useRef<HTMLDivElement>(null);
  const ytextRef = useRef<Y.Text | null>(null);
  const viewRef = useRef<EditorView | null>(null);
  const readOnlyCompartmentRef = useRef<Compartment | null>(null);
  const [previewHtml, setPreviewHtml] = useState("");
  const [readOnly, setReadOnly] = useState(false);
  const [syncMsg, setSyncMsg] = useState("");
  const [gitConsoleOpen, setGitConsoleOpen] = useState(false);
  const [gitConsoleText, setGitConsoleText] = useState("");
  const [gitConsoleErr, setGitConsoleErr] = useState<string | null>(null);
  const [gitConsoleLoading, setGitConsoleLoading] = useState(false);
  const [gitRepoMeta, setGitRepoMeta] = useState<{
    repo_url: string;
    branch: string;
    display_name: string;
    space_key: string;
  } | null>(null);
  /** Lines from Commit/push in this browser session (newest first). */
  const [pushLog, setPushLog] = useState<string[]>([]);

  useEffect(() => {
    let cancelled = false;
    let view: EditorView | null = null;
    const ydoc = new Y.Doc();
    const ytext = ydoc.getText("md");
    ytextRef.current = ytext;
    const readOnlyCompartment = new Compartment();
    readOnlyCompartmentRef.current = readOnlyCompartment;

    let bootHandled = false;
    let viewCreated = false;

    const debounce = { t: 0 as ReturnType<typeof setTimeout> | undefined };
    const previewPlugin = ViewPlugin.fromClass(
      class {
        update(u: ViewUpdate) {
          if (!u.docChanged) return;
          clearTimeout(debounce.t);
          debounce.t = setTimeout(() => {
            const md = u.state.doc.toString();
            renderGFM(md).then(setPreviewHtml).catch(() => {});
          }, 200);
        }
      }
    );

    const wsUrl = `${location.protocol === "https:" ? "wss:" : "ws:"}//${location.host}/ws?space=${encodeURIComponent(space)}&page=${encodeURIComponent(path)}`;
    const ws = new WebSocket(wsUrl);
    ws.binaryType = "arraybuffer";

    const applyReadOnly = (ro: boolean) => {
      setReadOnly(ro);
      const v = viewRef.current;
      if (!v) return;
      v.dispatch({
        effects: readOnlyCompartment.reconfigure(EditorState.readOnly.of(ro)),
      });
    };

    function buildExtensions() {
      return [
        readOnlyCompartment.of(EditorState.readOnly.of(false)),
        lineNumbers(),
        EditorView.lineWrapping,
        yCollab(ytext, null, { undoManager: false }),
        keymap.of(defaultKeymap),
        previewPlugin,
        EditorView.theme({
          "&": { height: "100%", minHeight: "320px" },
          ".cm-scroller": {
            fontFamily:
              'ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif',
            fontSize: "15px",
            lineHeight: 1.55,
          },
          ".cm-content": { caretColor: "#e6edf3" },
          ".cm-gutters": {
            background: "#161b22",
            color: "#6e7681",
            border: "none",
          },
        }),
      ];
    }

    function createEditorView() {
      if (viewCreated || cancelled || !host.current) return;
      viewCreated = true;
      const state = EditorState.create({
        doc: ytext.toString(),
        extensions: buildExtensions(),
      });
      view = new EditorView({ state, parent: host.current });
      viewRef.current = view;
      renderGFM(ytext.toString()).then(setPreviewHtml).catch(() => {});
    }

    /** Load file from API — only for leader, or joiner fallback if alone. */
    async function seedFromHttp(): Promise<void> {
      const res = await fetch(
        `/api/spaces/${encodeURIComponent(space)}/page?path=${encodeURIComponent(path)}`,
        { credentials: "include" }
      );
      if (!res.ok || cancelled) return;
      const j = (await res.json()) as { content?: string };
      if (ytext.length > 0) return;
      const initial = typeof j.content === "string" ? j.content : DEFAULT_PAGE_TEXT;
      ydoc.transact(() => {
        ytext.insert(0, initial);
      }, ORIGIN_INIT);
    }

    /** Peer asks for full Yjs state for another tab (joiner). */
    async function sendStateBlob(forClient: string) {
      const start = Date.now();
      while (ytext.length === 0 && Date.now() - start < 8000 && !cancelled) {
        await new Promise((r) => setTimeout(r, 40));
      }
      if (cancelled || ws.readyState !== WebSocket.OPEN) return;
      const u = Y.encodeStateAsUpdate(ydoc);
      ws.send(
        JSON.stringify({
          type: "state_blob",
          for_client: forClient,
          data_b64: uint8ToBase64(u),
        })
      );
    }

    async function onLeaderBoot() {
      await seedFromHttp();
      if (cancelled || !host.current) return;
      createEditorView();
    }

    /** Joiner: empty editor first; state arrives via Yjs. Fallback HTTP only if still empty (solo / no peer). */
    function onJoinerBoot(): () => void {
      createEditorView();
      const joinerFallback = setTimeout(async () => {
        if (cancelled || ytext.length > 0) return;
        await seedFromHttp();
        if (cancelled || !view) return;
        view.dispatch({
          changes: { from: 0, to: view.state.doc.length, insert: ytext.toString() },
        });
        renderGFM(ytext.toString()).then(setPreviewHtml).catch(() => {});
      }, 8000);
      return () => clearTimeout(joinerFallback);
    }

    let cleanupJoiner: (() => void) | undefined;

    ws.onopen = () => {
      ws.send(JSON.stringify({ type: "need_sync" }));
    };

    ws.onmessage = (ev) => {
      if (ev.data instanceof ArrayBuffer) {
        Y.applyUpdate(ydoc, new Uint8Array(ev.data), "remote");
        if (!viewCreated && ytext.length > 0 && host.current) {
          createEditorView();
        }
        return;
      }
      let ctrl: { type?: string; reason?: string; for_client?: string };
      try {
        ctrl = JSON.parse(String(ev.data));
      } catch {
        return;
      }

      if (ctrl.type === "request_state" && ctrl.for_client) {
        void sendStateBlob(ctrl.for_client);
        return;
      }

      if (!bootHandled) {
        if (ctrl.type === "sync_ok") {
          bootHandled = true;
          void onLeaderBoot();
        } else if (ctrl.type === "sync_lock") {
          bootHandled = true;
          cleanupJoiner = onJoinerBoot();
        }
      }

      if (ctrl.type === "sync_lock") {
        setSyncMsg(ctrl.reason ?? "locked until peer sync");
        applyReadOnly(true);
      }
      if (ctrl.type === "sync_ok") {
        setSyncMsg("");
        applyReadOnly(false);
      }
    };

    ydoc.on("update", (update, origin) => {
      if (origin === "remote" || origin === ORIGIN_INIT) return;
      if (ws.readyState !== WebSocket.OPEN) return;
      ws.send(update);
    });

    return () => {
      cancelled = true;
      cleanupJoiner?.();
      readOnlyCompartmentRef.current = null;
      viewRef.current = null;
      ws.close();
      view?.destroy();
    };
  }, [space, path]);

  const refreshGitConsole = useCallback(async () => {
    setGitConsoleErr(null);
    setGitConsoleLoading(true);
    try {
      const r = await fetch(`/api/spaces/${encodeURIComponent(space)}/git`, {
        credentials: "include",
      });
      const raw = await r.text();
      if (!r.ok) {
        setGitConsoleErr(raw || r.statusText);
        setGitConsoleText("");
        setGitRepoMeta(null);
        return;
      }
      const j = JSON.parse(raw) as {
        output?: string;
        repo_url?: string;
        branch?: string;
        display_name?: string;
        space_key?: string;
      };
      setGitConsoleText(j.output ?? "");
      if (j.repo_url) {
        setGitRepoMeta({
          repo_url: j.repo_url,
          branch: j.branch ?? "",
          display_name: j.display_name ?? "",
          space_key: j.space_key ?? space,
        });
      }
    } catch (e) {
      setGitConsoleErr(e instanceof Error ? e.message : "request failed");
      setGitConsoleText("");
      setGitRepoMeta(null);
    } finally {
      setGitConsoleLoading(false);
    }
  }, [space]);

  useEffect(() => {
    if (gitConsoleOpen) {
      void refreshGitConsole();
    }
  }, [gitConsoleOpen, space, refreshGitConsole]);

  useEffect(() => {
    setPushLog([]);
  }, [space]);

  const save = async () => {
    const body = ytextRef.current?.toString() ?? "";
    const r = await fetch(`/api/spaces/${encodeURIComponent(space)}/page`, {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        path,
        content: body,
        co_authors: [`${userName} <local@mdwiki>`],
      }),
    });
    const ct = r.headers.get("content-type") ?? "";
    if (r.ok) {
      if (ct.includes("application/json")) {
        try {
          const j = (await r.json()) as {
            ok?: boolean;
            path?: string;
            commit?: string;
            message?: string;
            repo_url?: string;
            branch?: string;
            display_name?: string;
          };
          const line = `${new Date().toLocaleTimeString()}  OK  ${j.path ?? path}  ${j.commit ?? "?"}  ${j.message ?? "pushed"}`;
          if (j.repo_url) {
            setGitRepoMeta({
              repo_url: j.repo_url,
              branch: j.branch ?? "",
              display_name: j.display_name ?? "",
              space_key: space,
            });
          }
          setPushLog((prev) => [line, ...prev].slice(0, 25));
        } catch {
          setPushLog((prev) =>
            [`${new Date().toLocaleTimeString()}  OK  (saved; refresh git console for details)`, ...prev].slice(0, 25),
          );
        }
      } else {
        setPushLog((prev) =>
          [`${new Date().toLocaleTimeString()}  OK  (saved)`, ...prev].slice(0, 25),
        );
      }
      await refreshGitConsole();
    } else {
      const errText = await r.text();
      setPushLog((prev) =>
        [`${new Date().toLocaleTimeString()}  ERROR ${r.status}  ${errText || r.statusText}`, ...prev].slice(0, 25),
      );
    }
  };

  const activeSpace = spaces.find((s) => s.key === space);

  if (spaces.length === 0) {
    return (
      <div style={{ padding: 24, maxWidth: 560 }}>
        <h1 style={{ marginTop: 0 }}>No spaces configured</h1>
        <p>
          Add a <code>spaces:</code> list to <code>spaces-registry.yaml</code> (see{" "}
          <code>spaces-registry.yaml</code> in the repo) and set <code>MDWIKI_REGISTRY</code> if needed.
          Each entry needs <code>key</code>, <code>display_name</code>, <code>repo_url</code>, and{" "}
          <code>branch</code>.
        </p>
        <button type="button" onClick={onLogout}>
          Log out
        </button>
      </div>
    );
  }

  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        height: "100vh",
        overflow: "hidden",
        background: "#0d1117",
      }}
    >
      <header
        style={{
          display: "flex",
          flexWrap: "wrap",
          alignItems: "center",
          gap: 12,
          padding: "8px 16px",
          borderBottom: "1px solid #30363d",
          background: "#161b22",
        }}
      >
        <strong>mdwiki</strong>
        <label style={{ display: "flex", alignItems: "center", gap: 8 }}>
          Space
          <select
            value={space}
            onChange={(e) => onSpaceChange(e.target.value)}
            style={{ minWidth: 180 }}
          >
            {spaces.map((s) => (
              <option key={s.key} value={s.key}>
                {s.display_name || s.key}
              </option>
            ))}
          </select>
        </label>
        {activeSpace?.repo_url ? (
          <span style={{ fontSize: 13, color: "#8b949e" }}>
            Repo{" "}
            <a href={activeSpace.repo_url} target="_blank" rel="noreferrer" style={{ color: "#58a6ff" }}>
              {activeSpace.repo_url.replace(/^https?:\/\//, "").replace(/\.git$/, "")}
            </a>
            {activeSpace.branch ? (
              <span style={{ marginLeft: 8 }}>
                branch <code style={{ color: "#c9d1d9" }}>{activeSpace.branch}</code>
              </span>
            ) : null}
          </span>
        ) : null}
        <label style={{ display: "flex", alignItems: "center", gap: 8 }}>
          Page
          <input
            value={path}
            onChange={(e) => onPathChange(e.target.value)}
            placeholder="README.md"
            style={{ width: 260 }}
          />
        </label>
        {readOnly && (
          <span style={{ color: "#d29922" }}>Read-only: {syncMsg}</span>
        )}
        <span style={{ flex: 1 }} />
        <button
          type="button"
          onClick={() => setGitConsoleOpen((o) => !o)}
          aria-expanded={gitConsoleOpen}
          title="Show or hide git branch, status, and recent commits"
        >
          {gitConsoleOpen ? "Hide git console" : "Git console"}
        </button>
        <button type="button" onClick={save}>
          Commit / push
        </button>
        <button type="button" onClick={onLogout}>
          Log out
        </button>
      </header>
      <div
        style={{
          flex: 1,
          minHeight: 0,
          display: "grid",
          gridTemplateColumns: "1fr 1fr",
          gridTemplateRows: "auto 1fr",
          gap: 0,
          overflow: "hidden",
        }}
      >
        <div
          style={{
            gridColumn: "1",
            padding: "6px 12px",
            fontSize: 12,
            color: "#8b949e",
            borderRight: "1px solid #30363d",
            borderBottom: "1px solid #21262d",
            background: "#0d1117",
          }}
        >
          Edit (wiki source — saved as Markdown in git)
        </div>
        <div
          style={{
            gridColumn: "2",
            padding: "6px 12px",
            fontSize: 12,
            color: "#8b949e",
            borderBottom: "1px solid #21262d",
            background: "#0d1117",
          }}
        >
          Preview
        </div>
        <div
          ref={host}
          style={{
            gridColumn: "1",
            gridRow: "2",
            borderRight: "1px solid #30363d",
            minHeight: 0,
          }}
        />
        <div
          style={{
            gridColumn: "2",
            gridRow: "2",
            overflow: "auto",
            padding: 16,
            background: "#0d1117",
          }}
        >
          <div
            className="md-preview"
            dangerouslySetInnerHTML={{ __html: previewHtml }}
          />
          <DiagramPreview html={previewHtml} />
        </div>
      </div>
      {gitConsoleOpen ? (
        <div
          style={{
            flexShrink: 0,
            borderTop: "1px solid #30363d",
            background: "#010409",
            maxHeight: "min(40vh, 360px)",
            display: "flex",
            flexDirection: "column",
          }}
        >
          <div
            style={{
              padding: "6px 12px",
              display: "flex",
              flexWrap: "wrap",
              alignItems: "center",
              gap: 8,
              borderBottom: "1px solid #21262d",
              fontSize: 12,
              color: "#8b949e",
            }}
          >
            <strong style={{ color: "#c9d1d9" }}>Git</strong>
            <span>repo · commit/push · working tree</span>
            <span style={{ flex: 1 }} />
            <button type="button" onClick={() => void refreshGitConsole()} disabled={gitConsoleLoading}>
              Refresh
            </button>
            <button
              type="button"
              onClick={() => {
                const repo = gitRepoMeta?.repo_url ?? activeSpace?.repo_url ?? "";
                const br = gitRepoMeta?.branch ?? activeSpace?.branch ?? "";
                const repoBlock = `=== repository ===\n${repo}\nbranch: ${br}\n\n`;
                const pushBlock =
                  pushLog.length > 0
                    ? `=== commit / push (this session) ===\n${pushLog.join("\n")}\n\n`
                    : "";
                const body = gitConsoleErr
                  ? gitConsoleErr + "\n\n"
                  : gitConsoleLoading
                    ? "Loading working tree…\n"
                    : gitConsoleText || "";
                void navigator.clipboard.writeText(repoBlock + pushBlock + body);
              }}
            >
              Copy
            </button>
          </div>
          <div
            style={{
              overflow: "auto",
              flex: 1,
              padding: 12,
              fontSize: 12,
              lineHeight: 1.45,
              color: "#c9d1d9",
              fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace',
            }}
          >
            <div style={{ color: "#8b949e", marginBottom: 6 }}>Repository</div>
            <div style={{ marginBottom: 12, wordBreak: "break-all" }}>
              {gitRepoMeta?.repo_url || activeSpace?.repo_url || "(open Refresh to load)"}
              <br />
              <span style={{ color: "#8b949e" }}>
                branch {gitRepoMeta?.branch || activeSpace?.branch || "—"}
                {gitRepoMeta?.display_name ? ` · ${gitRepoMeta.display_name}` : ""}
              </span>
            </div>
            <div style={{ color: "#8b949e", marginBottom: 6 }}>Commit / push (this session)</div>
            <div style={{ marginBottom: 12 }}>
              {pushLog.length > 0 ? (
                pushLog.map((line, i) => (
                  <div
                    key={`${i}-${line.slice(0, 24)}`}
                    style={{ color: line.includes("ERROR") ? "#f85149" : "#56d364", whiteSpace: "pre-wrap", wordBreak: "break-word" }}
                  >
                    {line}
                  </div>
                ))
              ) : (
                <span style={{ color: "#8b949e" }}>— (use Commit / push above)</span>
              )}
            </div>
            <div style={{ color: "#8b949e", marginBottom: 6 }}>Working tree (git CLI)</div>
            {gitConsoleErr ? (
              <pre style={{ margin: 0, color: "#f85149", whiteSpace: "pre-wrap" }}>{gitConsoleErr}</pre>
            ) : gitConsoleLoading ? (
              <span style={{ color: "#8b949e" }}>Loading…</span>
            ) : (
              <pre style={{ margin: 0, whiteSpace: "pre-wrap", wordBreak: "break-word" }}>
                {gitConsoleText || "(empty)"}
              </pre>
            )}
          </div>
        </div>
      ) : null}
    </div>
  );
}
