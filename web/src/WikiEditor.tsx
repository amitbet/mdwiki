import Collaboration from "@tiptap/extension-collaboration";
import Link from "@tiptap/extension-link";
import StarterKit from "@tiptap/starter-kit";
import Underline from "@tiptap/extension-underline";
import { EditorContent, useEditor } from "@tiptap/react";
import { useCallback, useEffect, useMemo, useState } from "react";
import * as Y from "yjs";
import { htmlToMarkdown } from "./md/htmlToMarkdown";
import { renderGFM } from "./md/render";

export type SpaceInfo = {
  key: string;
  display_name: string;
  repo_url: string;
  branch: string;
};

type PageTreeNode = {
  name: string;
  path: string;
  type: "folder" | "page";
  children?: PageTreeNode[];
};

type Props = {
  spaces: SpaceInfo[];
  space: string;
  onSpaceChange: (key: string) => void;
  path: string;
  onPathChange: (p: string) => void;
};

const DEFAULT_PAGE_TEXT = "# New Page\n\nStart writing here.\n";

function pageTitle(path: string): string {
  const name = path.split("/").pop() ?? path;
  return name.replace(/\.md$/i, "");
}

function uint8ToBase64(u8: Uint8Array): string {
  let binary = "";
  for (let i = 0; i < u8.length; i++) {
    binary += String.fromCharCode(u8[i]!);
  }
  return btoa(binary);
}

function Tree({
  nodes,
  activePath,
  onSelect,
}: {
  nodes: PageTreeNode[];
  activePath: string;
  onSelect: (p: string) => void;
}) {
  return (
    <ul className="wiki-tree">
      {nodes.map((n) => (
        <li key={`${n.type}:${n.path}`}>
          {n.type === "folder" ? (
            <details open>
              <summary>{n.name}</summary>
              <Tree nodes={n.children ?? []} activePath={activePath} onSelect={onSelect} />
            </details>
          ) : (
            <button
              type="button"
              className={n.path === activePath ? "tree-page active" : "tree-page"}
              onClick={() => onSelect(n.path)}
            >
              {n.name}
            </button>
          )}
        </li>
      ))}
    </ul>
  );
}

export default function WikiEditor({ spaces, space, onSpaceChange, path, onPathChange }: Props) {
  const [tree, setTree] = useState<PageTreeNode[]>([]);
  const [markdown, setMarkdown] = useState(DEFAULT_PAGE_TEXT);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [status, setStatus] = useState<string>("");
  const [readOnly, setReadOnly] = useState(false);
  const [syncMsg, setSyncMsg] = useState("");
  const [contextMenu, setContextMenu] = useState<{
    x: number;
    y: number;
    position: number;
  } | null>(null);

  const ydoc = useMemo(() => new Y.Doc(), [space, path]);

  const editor = useEditor(
    {
      extensions: [
        StarterKit.configure({ undoRedo: false }),
        Underline,
        Link.configure({ openOnClick: false }),
        Collaboration.configure({
          document: ydoc,
          field: "content",
        }),
      ],
      content: "<p>Loading…</p>",
      editorProps: {
        attributes: {
          class: "wysiwyg-editor md-preview",
        },
      },
      onUpdate({ editor: ed }) {
        setMarkdown(htmlToMarkdown(ed.getHTML()));
      },
    },
    [ydoc],
  );

  useEffect(() => {
    if (!editor) {
      return;
    }
    editor.setEditable(!readOnly);
  }, [editor, readOnly]);

  const loadTree = useCallback(async () => {
    try {
      const r = await fetch(`/api/spaces/${encodeURIComponent(space)}/pages`, { credentials: "include" });
      if (!r.ok) {
        throw new Error(await r.text());
      }
      const j = (await r.json()) as { tree?: PageTreeNode[] };
      setTree(Array.isArray(j.tree) ? j.tree : []);
    } catch (e) {
      setError(e instanceof Error ? e.message : "failed to load page index");
      setTree([]);
    }
  }, [space]);

  const seedFromHttp = useCallback(async () => {
    if (!editor) {
      return;
    }
    const fragment = ydoc.getXmlFragment("content");
    if (fragment.length > 0) {
      return;
    }
    const r = await fetch(`/api/spaces/${encodeURIComponent(space)}/page?path=${encodeURIComponent(path)}`, {
      credentials: "include",
    });
    if (!r.ok) {
      return;
    }
    const j = (await r.json()) as { content?: string };
    const md = typeof j.content === "string" ? j.content : DEFAULT_PAGE_TEXT;
    const html = await renderGFM(md);
    editor.commands.setContent(html, { emitUpdate: true });
    setMarkdown(md);
  }, [editor, path, space, ydoc]);

  useEffect(() => {
    void loadTree();
  }, [loadTree]);

  useEffect(() => {
    if (!editor) {
      return;
    }
    let cancelled = false;
    let bootHandled = false;

    const wsUrl = `${location.protocol === "https:" ? "wss:" : "ws:"}//${location.host}/ws?space=${encodeURIComponent(space)}&page=${encodeURIComponent(path)}`;
    const ws = new WebSocket(wsUrl);
    ws.binaryType = "arraybuffer";

    const updateHandler = (update: Uint8Array, origin: unknown) => {
      if (origin === "remote") {
        return;
      }
      if (ws.readyState !== WebSocket.OPEN) {
        return;
      }
      ws.send(update);
    };
    ydoc.on("update", updateHandler);

    async function sendStateBlob(forClient: string) {
      const start = Date.now();
      while (!cancelled && ydoc.getXmlFragment("content").length === 0 && Date.now() - start < 8000) {
        await new Promise((r) => setTimeout(r, 40));
      }
      if (cancelled || ws.readyState !== WebSocket.OPEN) {
        return;
      }
      const u = Y.encodeStateAsUpdate(ydoc);
      ws.send(
        JSON.stringify({
          type: "state_blob",
          for_client: forClient,
          data_b64: uint8ToBase64(u),
        }),
      );
    }

    ws.onopen = () => {
      ws.send(JSON.stringify({ type: "need_sync" }));
    };

    ws.onmessage = (ev) => {
      if (ev.data instanceof ArrayBuffer) {
        Y.applyUpdate(ydoc, new Uint8Array(ev.data), "remote");
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
          void seedFromHttp();
        } else if (ctrl.type === "sync_lock") {
          bootHandled = true;
          setTimeout(() => {
            if (cancelled || ydoc.getXmlFragment("content").length > 0) {
              return;
            }
            void seedFromHttp();
          }, 8000);
        }
      }

      if (ctrl.type === "sync_lock") {
        setReadOnly(true);
        setSyncMsg(ctrl.reason ?? "locked until peer sync");
      } else if (ctrl.type === "sync_ok") {
        setReadOnly(false);
        setSyncMsg("");
      }
    };

    ws.onerror = () => {
      setError("realtime sync connection failed");
    };

    return () => {
      cancelled = true;
      ydoc.off("update", updateHandler);
      ws.close();
    };
  }, [editor, path, seedFromHttp, space, ydoc]);

  const save = useCallback(async () => {
    setSaving(true);
    setError(null);
    setStatus("");
    try {
      const body = editor ? htmlToMarkdown(editor.getHTML()) : markdown;
      const r = await fetch(`/api/spaces/${encodeURIComponent(space)}/page`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({
          path,
          content: body,
          co_authors: [],
        }),
      });
      if (!r.ok) {
        throw new Error(await r.text());
      }
      setStatus(`Saved ${new Date().toLocaleTimeString()}`);
      await loadTree();
    } catch (e) {
      setError(e instanceof Error ? e.message : "save failed");
    } finally {
      setSaving(false);
    }
  }, [editor, loadTree, markdown, path, space]);

  const createPage = useCallback(async () => {
    const suggestion = path.includes("/")
      ? `${path.slice(0, path.lastIndexOf("/") + 1)}new-page.md`
      : "new-page.md";
    const nextPath = window.prompt("New page path (relative .md path)", suggestion);
    if (!nextPath) {
      return;
    }
    try {
      setError(null);
      const r = await fetch(`/api/spaces/${encodeURIComponent(space)}/pages`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({ path: nextPath }),
      });
      if (!r.ok) {
        throw new Error(await r.text());
      }
      const j = (await r.json()) as { path?: string; content?: string };
      if (j.path) {
        onPathChange(j.path);
      }
      if (typeof j.content === "string") {
        const html = await renderGFM(j.content);
        editor?.commands.setContent(html, { emitUpdate: true });
      }
      await loadTree();
    } catch (e) {
      setError(e instanceof Error ? e.message : "failed to create page");
    }
  }, [editor, loadTree, onPathChange, path, space]);

  const insertLink = useCallback(() => {
    if (!editor) {
      return;
    }
    const previousUrl = editor.getAttributes("link").href as string | undefined;
    const url = window.prompt("Link URL", previousUrl ?? "https://");
    if (url === null) {
      return;
    }
    if (url.trim() === "") {
      editor.chain().focus().unsetLink().run();
      return;
    }
    editor.chain().focus().setLink({ href: url.trim() }).run();
  }, [editor]);

  const addComment = useCallback(async () => {
    if (!editor || !contextMenu) {
      return;
    }
    const comment = window.prompt("Comment");
    if (!comment || comment.trim().length === 0) {
      setContextMenu(null);
      return;
    }
    const anchorID = `a_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`;
    editor.chain().focus(contextMenu.position).insertContent(`<span data-wiki-anchor="${anchorID}"></span>`).run();
    const body = htmlToMarkdown(editor.getHTML());
    setMarkdown(body);
    try {
      const r = await fetch(`/api/spaces/${encodeURIComponent(space)}/comments`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          path,
          anchor_id: anchorID,
          comment: comment.trim(),
          position: contextMenu.position,
        }),
      });
      if (!r.ok) {
        throw new Error(await r.text());
      }
      // Save page and comment file in one commit (save stages all working-tree changes).
      const saveRes = await fetch(`/api/spaces/${encodeURIComponent(space)}/page`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          path,
          content: body,
          co_authors: [],
        }),
      });
      if (!saveRes.ok) {
        throw new Error(await saveRes.text());
      }
      setStatus(`Comment added ${new Date().toLocaleTimeString()}`);
    } catch (e) {
      setError(e instanceof Error ? e.message : "failed to add comment");
    } finally {
      setContextMenu(null);
      await loadTree();
    }
  }, [contextMenu, editor, loadTree, path, space]);

  return (
    <div className="wiki-shell">
      <header className="wiki-topbar">
        <div className="brand">mdwiki</div>
        <label>
          Space
          <select value={space} onChange={(e) => onSpaceChange(e.target.value)}>
            {spaces.map((s) => (
              <option key={s.key} value={s.key}>
                {s.display_name || s.key}
              </option>
            ))}
          </select>
        </label>
        <button type="button" className="plus-btn" onClick={() => void createPage()} title="Create page">
          +
        </button>
        <div className="active-page">{pageTitle(path)}</div>
        <div className="spacer" />
        {readOnly ? <span className="sync-badge">Read-only: {syncMsg}</span> : null}
        <button type="button" onClick={() => void save()} disabled={saving || readOnly}>
          {saving ? "Saving…" : "Save"}
        </button>
      </header>

      <div className="wiki-body">
        <aside className="wiki-sidebar">
          <div className="sidebar-title">Pages</div>
          <Tree nodes={tree} activePath={path} onSelect={onPathChange} />
        </aside>

        <main className="wiki-main">
          <div className="editor-toolbar">
            <button type="button" onClick={() => editor?.chain().focus().toggleBold().run()}>B</button>
            <button type="button" onClick={() => editor?.chain().focus().toggleItalic().run()}>I</button>
            <button type="button" onClick={() => editor?.chain().focus().toggleUnderline().run()}>U</button>
            <button type="button" onClick={() => editor?.chain().focus().toggleHeading({ level: 1 }).run()}>
              H1
            </button>
            <button type="button" onClick={() => editor?.chain().focus().toggleHeading({ level: 2 }).run()}>
              H2
            </button>
            <button type="button" onClick={() => editor?.chain().focus().toggleBulletList().run()}>
              • List
            </button>
            <button type="button" onClick={() => editor?.chain().focus().toggleOrderedList().run()}>
              1. List
            </button>
            <button type="button" onClick={() => editor?.chain().focus().toggleBlockquote().run()}>
              Quote
            </button>
            <button type="button" onClick={() => editor?.chain().focus().toggleCodeBlock().run()}>
              Code
            </button>
            <button type="button" onClick={insertLink}>Link</button>
          </div>

          <div
            className="editor-container"
            onContextMenu={(e) => {
              if (!editor) {
                return;
              }
              e.preventDefault();
              const pos = editor.state.selection.anchor;
              setContextMenu({ x: e.clientX, y: e.clientY, position: pos });
            }}
            onClick={() => {
              if (contextMenu) {
                setContextMenu(null);
              }
            }}
          >
            <EditorContent editor={editor} />
          </div>
          {contextMenu ? (
            <div className="editor-context-menu" style={{ left: contextMenu.x, top: contextMenu.y }}>
              <button type="button" onClick={() => void addComment()}>
                Add comment
              </button>
            </div>
          ) : null}

          <div className="editor-status">
            {status || "Rendered markdown editor"}
            {error ? <span className="error"> · {error}</span> : null}
          </div>
        </main>
      </div>
    </div>
  );
}
