import Collaboration from "@tiptap/extension-collaboration";
import CodeBlockLowlight from "@tiptap/extension-code-block-lowlight";
import Highlight from "@tiptap/extension-highlight";
import Image from "@tiptap/extension-image";
import Link from "@tiptap/extension-link";
import StarterKit from "@tiptap/starter-kit";
import Underline from "@tiptap/extension-underline";
import { EditorContent, useEditor } from "@tiptap/react";
import FormatBoldIcon from "@mui/icons-material/FormatBold";
import FormatItalicIcon from "@mui/icons-material/FormatItalic";
import FormatUnderlinedIcon from "@mui/icons-material/FormatUnderlined";
import InsertLinkIcon from "@mui/icons-material/InsertLink";
import StrikethroughSIcon from "@mui/icons-material/StrikethroughS";
import { createLowlight } from "lowlight";
import bash from "highlight.js/lib/languages/bash";
import css from "highlight.js/lib/languages/css";
import go from "highlight.js/lib/languages/go";
import html from "highlight.js/lib/languages/xml";
import javascript from "highlight.js/lib/languages/javascript";
import json from "highlight.js/lib/languages/json";
import markdown from "highlight.js/lib/languages/markdown";
import plaintext from "highlight.js/lib/languages/plaintext";
import typescript from "highlight.js/lib/languages/typescript";
import yaml from "highlight.js/lib/languages/yaml";
import { type MouseEvent as ReactMouseEvent, type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react";
import * as Y from "yjs";
import { DiagramPreview } from "./DiagramPreview";
import { htmlToMarkdown } from "./md/htmlToMarkdown";
import { renderGFM } from "./md/render";

export type SpaceInfo = {
  key: string;
  display_name: string;
  created_by_login?: string;
  repo_url: string;
  branch: string;
};

type PageTreeNode = {
  name: string;
  path: string;
  type: "folder" | "page";
  children?: PageTreeNode[];
};

type CreateParentOption = {
  value: string;
  label: string;
  prefix: string;
};

type CommentMessage = {
  hash_id: string;
  position: number;
  author_id: string;
  body: string;
  created_at: string;
  updated_at: string;
  replaces?: string | null;
  in_reply_to?: string | null;
  can_edit: boolean;
};

type CommentThread = {
  anchor_id: string;
  thread_id: string;
  status?: string;
  messages: CommentMessage[];
};

function visibleThreadMessages(messages: CommentMessage[]): CommentMessage[] {
  const byID = new Map<string, CommentMessage>();
  const replaced = new Set<string>();
  for (const m of messages) {
    byID.set(m.hash_id, m);
    if (m.replaces) {
      replaced.add(m.replaces);
    }
  }
  const out = messages.filter((m) => !replaced.has(m.hash_id));
  out.sort((a, b) => {
    const ta = Date.parse(a.updated_at || a.created_at) || 0;
    const tb = Date.parse(b.updated_at || b.created_at) || 0;
    return ta - tb;
  });
  return out;
}

type Props = {
  spaces: SpaceInfo[];
  space: string;
  onSpaceChange: (key: string) => void;
  onSpacesChanged: () => Promise<void> | void;
  currentUserLogin: string;
  path: string;
  onPathChange: (p: string) => void;
  theme: "light" | "dark";
  onToggleTheme: () => void;
  currentPagePendingSave: PendingSaveJob | null;
  onQueueSave: (job: PendingSaveJob) => void;
};

type SettingsInfo = {
  settings?: {
    root_repo_url?: string;
    root_repo_local_dir?: string;
    storage_dir?: string;
    save_mode?: "local" | "git_sync";
  };
  storage?: {
    implementation?: string;
    local_settings?: string;
    root_settings?: string;
    storage_dir?: string;
  };
};

type DraftInfo = {
  exists: boolean;
  base_commit?: string;
  current_base_commit?: string;
  updated_at?: string;
  format?: string;
  update_b64?: string;
  markdown?: string;
  base_changed?: boolean;
};

type DiagramEditorState = {
  path: string;
  kind: "excalidraw" | "drawio";
  content: string;
};

type PendingNavigation =
  | { kind: "path"; value: string }
  | { kind: "space"; value: string }
  | null;

type RefreshPrompt = {
  path: string;
  commit?: string;
} | null;

export type PendingSaveJob = {
  jobId: string;
  space: string;
  path: string;
  status: "queued" | "running" | "succeeded" | "failed";
  message?: string;
  error?: string;
  commit?: string;
  snapshot?: string;
  updatedAt?: string;
};

const DEFAULT_PAGE_TEXT = "# New Page\n\nStart writing here.\n";
const CODE_LANGUAGES = [
  "plaintext",
  "javascript",
  "typescript",
  "json",
  "bash",
  "go",
  "html",
  "css",
  "markdown",
  "yaml",
  "mermaid",
  "chart",
  "graphviz",
  "dot",
  "vega-lite",
  "vega",
  "plantuml",
] as const;
const REALTIME_CONNECTION_FAILED_ERROR = "realtime sync connection failed";

const lowlight = createLowlight();
lowlight.register("plaintext", plaintext);
lowlight.register("javascript", javascript);
lowlight.register("typescript", typescript);
lowlight.register("json", json);
lowlight.register("bash", bash);
lowlight.register("go", go);
lowlight.register("html", html);
lowlight.register("css", css);
lowlight.register("markdown", markdown);
lowlight.register("yaml", yaml);
lowlight.register("mermaid", plaintext);
lowlight.register("chart", json);
lowlight.register("graphviz", plaintext);
lowlight.register("dot", plaintext);
lowlight.register("vega-lite", json);
lowlight.register("vega", json);
lowlight.register("plantuml", plaintext);

function canonicalMarkdown(input: string): string {
  return input
    .replace(/\r\n?/g, "\n")
    .replace(/[ \t]+$/gm, "")
    .replace(/\n{3,}/g, "\n\n")
    .trimEnd();
}

function escapeHTML(s: string): string {
  return s
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

function normalizeSlashPath(input: string): string {
  const segments = input
    .split("/")
    .filter((part) => part !== "" && part !== ".");
  const out: string[] = [];
  for (const part of segments) {
    if (part === "..") {
      if (out.length > 0) {
        out.pop();
      }
      continue;
    }
    out.push(part);
  }
  return out.join("/");
}

function pageDirectory(pagePath: string): string {
  const normalized = pagePath.replace(/^\/+/, "");
  const idx = normalized.lastIndexOf("/");
  return idx >= 0 ? normalized.slice(0, idx) : "";
}

function relativizeAssetPath(pagePath: string, assetPath: string): string {
  const assetParts = normalizeSlashPath(assetPath).split("/").filter(Boolean);
  const pageDirParts = pageDirectory(pagePath).split("/").filter(Boolean);
  let shared = 0;
  while (shared < pageDirParts.length && shared < assetParts.length && pageDirParts[shared] === assetParts[shared]) {
    shared += 1;
  }
  const upward = pageDirParts.slice(shared).map(() => "..");
  const downward = assetParts.slice(shared);
  return [...upward, ...downward].join("/") || assetParts.join("/");
}

function resolveAssetPath(pagePath: string, assetRef: string): string {
  const trimmed = assetRef.trim().replace(/^\/+/, "");
  if (trimmed === "") {
    return "";
  }
  const combined = trimmed.startsWith(".mdwiki/") || trimmed.startsWith("assets/")
    ? trimmed
    : [pageDirectory(pagePath), trimmed].filter(Boolean).join("/");
  return normalizeSlashPath(combined);
}

function assetApiURL(space: string, relPath: string): string {
  return `/api/spaces/${encodeURIComponent(space)}/asset?path=${encodeURIComponent(relPath)}`;
}

function diagramKindForPath(path: string): "excalidraw" | "drawio" | null {
  const lower = path.toLowerCase();
  if (lower.endsWith(".excalidraw")) {
    return "excalidraw";
  }
  if (lower.endsWith(".drawio")) {
    return "drawio";
  }
  return null;
}

function diagramPlaceholderHTML(path: string): string {
  const name = escapeHTML(path.split("/").pop() || path);
  const safePath = escapeHTML(path);
  return `<p><a href="${safePath}">Diagram: ${name}</a></p>`;
}

function isRelativeAssetPath(src: string): boolean {
  const trimmed = src.trim();
  if (!trimmed) {
    return false;
  }
  return !/^(https?:|data:|blob:|\/)/i.test(trimmed);
}

function pageTitle(path: string): string {
  const name = path.split("/").pop() ?? path;
  return name.replace(/\.md$/i, "");
}

function pageParentPrefix(pagePath: string): string {
  return pagePath.replace(/\.md$/i, "").replace(/^\/+/, "").replace(/\/+$/, "");
}

function firstPagePath(nodes: PageTreeNode[]): string | null {
  for (const node of nodes) {
    if (node.type === "page") {
      return node.path;
    }
    const nested = firstPagePath(node.children ?? []);
    if (nested) {
      return nested;
    }
  }
  return null;
}

function treeHasPagePath(nodes: PageTreeNode[], targetPath: string): boolean {
  for (const node of nodes) {
    if (node.type === "page" && node.path === targetPath) {
      return true;
    }
    if (node.children && treeHasPagePath(node.children, targetPath)) {
      return true;
    }
  }
  return false;
}

function mergePageAndFolderSiblings(nodes: PageTreeNode[]): PageTreeNode[] {
  const mergedChildren = nodes.map((node) => ({
    ...node,
    children: node.children ? mergePageAndFolderSiblings(node.children) : undefined,
  }));
  const pageByName = new Map<string, PageTreeNode>();
  for (const node of mergedChildren) {
    if (node.type === "page") {
      pageByName.set(node.name, node);
    }
  }
  const out: PageTreeNode[] = [];
  for (const node of mergedChildren) {
    if (node.type === "folder") {
      const page = pageByName.get(node.name);
      if (page) {
        page.children = node.children ?? [];
        continue;
      }
    }
    out.push(node);
  }
  return out;
}

function makeSpaceKey(displayName: string): string {
  const cleaned = displayName
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9-_ ]+/g, "")
    .replace(/\s+/g, "-")
    .replace(/^[^a-z0-9]+/, "")
    .replace(/-+/g, "-");
  return cleaned || "new-space";
}

function pickUniqueSpaceKey(baseKey: string, spaces: SpaceInfo[]): string {
  const taken = new Set(spaces.map((s) => s.key));
  if (!taken.has(baseKey)) {
    return baseKey;
  }
  for (let i = 2; i < 1000; i++) {
    const candidate = `${baseKey}-${i}`;
    if (!taken.has(candidate)) {
      return candidate;
    }
  }
  return `${baseKey}-${Date.now()}`;
}

function normalizeApiErrorMessage(raw: string): string {
  const msg = raw.trim();
  const lower = msg.toLowerCase();
  const isGitAuth =
    lower.includes("could not read username") ||
    lower.includes("authentication failed") ||
    lower.includes("authentication required") ||
    lower.includes("authorization failed");
  if (isGitAuth) {
    return `Git authentication failed. Sign in again, or configure MDWIKI_SERVER_GIT_TOKEN. Details: ${msg}`;
  }
  if (lower.includes("push failed") || lower.includes("failed to push") || lower.includes("non-fast-forward")) {
    return `Git push failed. ${msg}`;
  }
  return msg;
}

async function readApiError(resp: Response, fallback: string): Promise<string> {
  const text = (await resp.text()).trim();
  if (!text) {
    return fallback;
  }
  return normalizeApiErrorMessage(text);
}

function uint8ToBase64(u8: Uint8Array): string {
  let binary = "";
  for (let i = 0; i < u8.length; i++) {
    binary += String.fromCharCode(u8[i]!);
  }
  return btoa(binary);
}

function commentAnchorIdFromElement(el: HTMLElement | null): string {
  if (!el) {
    return "";
  }
  const direct = (el.getAttribute("data-wiki-comment") || "").trim();
  if (direct) {
    return direct;
  }
  const classes = (el.getAttribute("class") || "").split(/\s+/);
  for (const c of classes) {
    if (c.startsWith("wiki-comment-id-")) {
      const id = c.slice("wiki-comment-id-".length).trim();
      if (id) {
        return id;
      }
    }
  }
  return "";
}

function Tree({
  nodes,
  activePath,
  onSelect,
  onPageContextMenu,
  isCollapsed,
  onToggleCollapse,
}: {
  nodes: PageTreeNode[];
  activePath: string;
  onSelect: (p: string) => void;
  onPageContextMenu?: (e: ReactMouseEvent<HTMLElement>, p: string) => void;
  isCollapsed: (p: string) => boolean;
  onToggleCollapse: (p: string) => void;
}) {
  return (
    <ul className="wiki-tree">
      {nodes.map((n) => (
        <li key={`${n.type}:${n.path}`}>
          {n.type === "folder" ? (
            <details open>
              <summary>{n.name}</summary>
              <Tree
                nodes={n.children ?? []}
                activePath={activePath}
                onSelect={onSelect}
                onPageContextMenu={onPageContextMenu}
                isCollapsed={isCollapsed}
                onToggleCollapse={onToggleCollapse}
              />
            </details>
          ) : (
            <>
              <div className="tree-page-row" onContextMenu={(e) => onPageContextMenu?.(e, n.path)}>
                {n.children && n.children.length > 0 ? (
                  <button
                    type="button"
                    className="tree-expand-btn"
                    title={isCollapsed(n.path) ? "Expand children" : "Collapse children"}
                    aria-label={isCollapsed(n.path) ? "Expand children" : "Collapse children"}
                    onClick={() => onToggleCollapse(n.path)}
                  >
                    {isCollapsed(n.path) ? "▸" : "▾"}
                  </button>
                ) : (
                  <span className="tree-expand-placeholder" aria-hidden="true" />
                )}
                <button
                  type="button"
                  className={n.path === activePath ? "tree-page active" : "tree-page"}
                  onClick={() => onSelect(n.path)}
                  onContextMenu={(e) => onPageContextMenu?.(e, n.path)}
                >
                  {n.name}
                </button>
              </div>
              {n.children && n.children.length > 0 && !isCollapsed(n.path) ? (
                <Tree
                  nodes={n.children}
                  activePath={activePath}
                  onSelect={onSelect}
                  onPageContextMenu={onPageContextMenu}
                  isCollapsed={isCollapsed}
                  onToggleCollapse={onToggleCollapse}
                />
              ) : null}
            </>
          )}
        </li>
      ))}
    </ul>
  );
}

function IconButton({
  title,
  onClick,
  active,
  disabled,
  children,
}: {
  title: string;
  onClick: () => void;
  active?: boolean;
  disabled?: boolean;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      className={active ? "tool-btn is-active" : "tool-btn"}
      onClick={onClick}
      title={title}
      disabled={disabled}
    >
      {children}
    </button>
  );
}

function getLanguageFromCodeElement(pre: HTMLElement | null): string {
  if (!pre) {
    return "plaintext";
  }
  const code = pre.querySelector("code");
  const classNames = (code?.getAttribute("class") || "").split(/\s+/);
  for (const c of classNames) {
    if (c.startsWith("language-")) {
      return c.slice("language-".length) || "plaintext";
    }
  }
  return "plaintext";
}

function removeCommentHighlightMarks(editor: NonNullable<ReturnType<typeof useEditor>>, anchorId: string) {
  const markType = editor.state.schema.marks.highlight;
  if (!markType) {
    return;
  }
  let tr = editor.state.tr;
  editor.state.doc.descendants((node, pos) => {
    if (!node.isText || node.marks.length === 0) {
      return true;
    }
    for (const mark of node.marks) {
      if (mark.type === markType && (mark.attrs.commentId as string | undefined) === anchorId) {
        tr = tr.removeMark(pos, pos + node.nodeSize, mark);
      }
    }
    return true;
  });
  if (tr.docChanged) {
    editor.view.dispatch(tr);
  }
}

export default function WikiEditor({
  spaces,
  space,
  onSpaceChange,
  onSpacesChanged,
  currentUserLogin,
  path,
  onPathChange,
  theme,
  onToggleTheme,
  currentPagePendingSave,
  onQueueSave,
}: Props) {
  const [tree, setTree] = useState<PageTreeNode[]>([]);
  const [markdown, setMarkdown] = useState(DEFAULT_PAGE_TEXT);
  const [saving, setSaving] = useState(false);
  const [dirty, setDirty] = useState(false);
  const [isEditing, setIsEditing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [lastErrorDetails, setLastErrorDetails] = useState<string>("");
  const [errorDetailsOpen, setErrorDetailsOpen] = useState(false);
  const [consecutiveSaveFailures, setConsecutiveSaveFailures] = useState(0);
  const [status, setStatus] = useState<string>("");
  const [readOnly, setReadOnly] = useState(false);
  const [syncMsg, setSyncMsg] = useState("");
  const [threadsByAnchor, setThreadsByAnchor] = useState<Record<string, CommentThread>>({});
  const [contextMenu, setContextMenu] = useState<{
    x: number;
    y: number;
    from: number;
    to: number;
  } | null>(null);
  const [popover, setPopover] = useState<{ anchorId: string; x: number; y: number } | null>(null);
  const [codeLangHover, setCodeLangHover] = useState<{ x: number; y: number; pos: number; language: string } | null>(null);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [settingsInfo, setSettingsInfo] = useState<SettingsInfo | null>(null);
  const [settingsSaveMode, setSettingsSaveMode] = useState<"local" | "git_sync">("git_sync");
  const [settingsSaving, setSettingsSaving] = useState(false);
  const [createPageOpen, setCreatePageOpen] = useState(false);
  const [createPageInput, setCreatePageInput] = useState("new-page.md");
  const [createParent, setCreateParent] = useState("current");
  const [pageContextMenu, setPageContextMenu] = useState<{ x: number; y: number; pagePath: string } | null>(null);
  const [spaceContextMenu, setSpaceContextMenu] = useState<{ x: number; y: number } | null>(null);
  const [collapsedPageNodes, setCollapsedPageNodes] = useState<Record<string, boolean>>({});
  const [draftInfo, setDraftInfo] = useState<DraftInfo | null>(null);
  const [baseCommit, setBaseCommit] = useState("");
  const [compareDraftOpen, setCompareDraftOpen] = useState(false);
  const [diagramEditor, setDiagramEditor] = useState<DiagramEditorState | null>(null);
  const [pendingNavigation, setPendingNavigation] = useState<PendingNavigation>(null);
  const [navigationDecisionBusy, setNavigationDecisionBusy] = useState<"save" | "discard" | null>(null);
  const [refreshPrompt, setRefreshPrompt] = useState<RefreshPrompt>(null);
  const [asyncSaveState, setAsyncSaveState] = useState<"idle" | "queued" | "running" | "failed">("idle");
  const [editorSession, setEditorSession] = useState(0);
  const hideTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const codeLangHoverRef = useRef(false);
  const popoverHoverRef = useRef(false);
  const lastSavedMarkdownRef = useRef("");
  const applyingRemoteSyncRef = useRef(false);
  const suppressDirtyTrackingRef = useRef(false);
  const dirtyTrackingResumeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const wsReconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const wsReconnectAttemptsRef = useRef(0);
  const dirtyRef = useRef(false);
  const isEditingRef = useRef(isEditing);
  const markdownRef = useRef(markdown);
  const imageInputRef = useRef<HTMLInputElement | null>(null);
  const [wsReconnectTick, setWsReconnectTick] = useState(0);
  const canEdit = isEditing && !readOnly;
  const canComment = !readOnly;

  const ydoc = useMemo(() => new Y.Doc(), [editorSession, space, path]);
  const CommentHighlight = useMemo(
    () =>
      Highlight.extend({
        addAttributes() {
          return {
            ...this.parent?.(),
            commentId: {
              default: null,
              parseHTML: (element) => commentAnchorIdFromElement(element as HTMLElement),
              renderHTML: (attributes) => {
                const id = typeof attributes.commentId === "string" ? attributes.commentId : "";
                return id ? { "data-wiki-comment": id, class: `wiki-comment-highlight wiki-comment-id-${id}` } : {};
              },
            },
          };
        },
      }),
    [],
  );

  const editor = useEditor(
    {
      extensions: [
        StarterKit.configure({ undoRedo: false, codeBlock: false }),
        CodeBlockLowlight.configure({
          lowlight,
          languageClassPrefix: "language-",
          defaultLanguage: "plaintext",
        }),
        CommentHighlight,
        Underline,
        Image,
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
        handleKeyDown(view, event) {
          if (event.key !== "Tab") {
            return false;
          }
          event.preventDefault();
          const { from, to } = view.state.selection;
          view.dispatch(view.state.tr.insertText("\t", from, to).scrollIntoView());
          return true;
        },
      },
      onUpdate({ editor: ed }) {
        const next = htmlToMarkdown(ed.getHTML());
        markdownRef.current = next;
        setMarkdown(next);
        if (applyingRemoteSyncRef.current || suppressDirtyTrackingRef.current || !isEditingRef.current) {
          return;
        }
        setDirty(canonicalMarkdown(next) !== canonicalMarkdown(lastSavedMarkdownRef.current));
      },
    },
    [CommentHighlight, ydoc],
  );

  const commitCurrentState = useCallback(async () => {
    const body = editor ? htmlToMarkdown(editor.getHTML()) : markdown;
    if (canonicalMarkdown(body) === canonicalMarkdown(lastSavedMarkdownRef.current)) {
      return { mode: "sync" as const, message: "", commit: baseCommit };
    }
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
      throw new Error(await readApiError(r, "save failed"));
    }
    const j = (await r.json()) as { message?: string; commit?: string; queued?: boolean; job_id?: string };
    markdownRef.current = body;
    setMarkdown(body);
    lastSavedMarkdownRef.current = canonicalMarkdown(body);
    if (j.queued && typeof j.job_id === "string" && j.job_id) {
      onQueueSave({
        jobId: j.job_id,
        space,
        path,
        status: "queued",
        message: typeof j.message === "string" ? j.message : "Save queued",
        snapshot: body,
        updatedAt: new Date().toISOString(),
      });
      return {
        mode: "queued" as const,
        message: typeof j.message === "string" ? j.message : "Save queued",
        commit: baseCommit,
        jobId: j.job_id,
      };
    }
    const nextCommit = typeof j.commit === "string" ? j.commit : baseCommit;
    setBaseCommit(nextCommit);
    return {
      mode: "sync" as const,
      message: typeof j.message === "string" ? j.message : "",
      commit: nextCommit,
    };
  }, [baseCommit, editor, markdown, onQueueSave, path, space]);

  useEffect(() => {
    if (error) {
      setLastErrorDetails(error);
    }
  }, [error]);

  useEffect(() => {
    dirtyRef.current = dirty;
  }, [dirty]);

  useEffect(() => {
    markdownRef.current = markdown;
  }, [markdown]);

  useEffect(() => {
    isEditingRef.current = isEditing;
  }, [isEditing]);

  useEffect(() => {
    const onBeforeUnload = (e: BeforeUnloadEvent) => {
      if (!dirtyRef.current) {
        return;
      }
      e.preventDefault();
      e.returnValue = "";
    };
    window.addEventListener("beforeunload", onBeforeUnload);
    return () => {
      window.removeEventListener("beforeunload", onBeforeUnload);
    };
  }, []);

  const applyPendingNavigation = useCallback(
    (target: Exclude<PendingNavigation, null>) => {
      setPageContextMenu(null);
      if (target.kind === "path") {
        onPathChange(target.value);
        return;
      }
      onSpaceChange(target.value);
    },
    [onPathChange, onSpaceChange],
  );

  const requestNavigation = useCallback(
    (target: Exclude<PendingNavigation, null>) => {
      const isSameTarget = target.kind === "path" ? target.value === path : target.value === space;
      if (isSameTarget) {
        return;
      }
      if (!dirtyRef.current) {
        applyPendingNavigation(target);
        return;
      }
      setPendingNavigation(target);
    },
    [applyPendingNavigation, path, space],
  );

  const navigateToPath = useCallback(
    (nextPath: string) => {
      requestNavigation({ kind: "path", value: nextPath });
    },
    [requestNavigation],
  );

  const navigateToSpace = useCallback(
    (nextSpace: string) => {
      requestNavigation({ kind: "space", value: nextSpace });
    },
    [requestNavigation],
  );

  const suppressDirtyTrackingForTick = useCallback(() => {
    suppressDirtyTrackingRef.current = true;
    if (dirtyTrackingResumeTimerRef.current) {
      window.clearTimeout(dirtyTrackingResumeTimerRef.current);
    }
    dirtyTrackingResumeTimerRef.current = window.setTimeout(() => {
      suppressDirtyTrackingRef.current = false;
      dirtyTrackingResumeTimerRef.current = null;
    }, 0);
  }, []);

  useEffect(() => {
    if (!editor) {
      return;
    }
    editor.setEditable(canEdit);
  }, [canEdit, editor]);

  const applyTrustedMarkdown = useCallback(
    async (md: string) => {
      if (!editor) {
        return;
      }
      const normalizedIncoming = canonicalMarkdown(md);
      const normalizedCurrent = canonicalMarkdown(markdownRef.current);
      if (normalizedIncoming === normalizedCurrent && normalizedIncoming === canonicalMarkdown(lastSavedMarkdownRef.current)) {
        markdownRef.current = md;
        setMarkdown(md);
        setDirty(false);
        return;
      }
      const html = await renderGFM(md);
      suppressDirtyTrackingForTick();
      lastSavedMarkdownRef.current = normalizedIncoming;
      markdownRef.current = md;
      setMarkdown(md);
      setDirty(false);
      editor.commands.setContent(html, { emitUpdate: true });
    },
    [editor, suppressDirtyTrackingForTick],
  );

  const applyRecoveredMarkdown = useCallback(
    async (md: string) => {
      if (!editor) {
        return;
      }
      const html = await renderGFM(md);
      const restoredDirty = canonicalMarkdown(md) !== canonicalMarkdown(lastSavedMarkdownRef.current);
      suppressDirtyTrackingForTick();
      markdownRef.current = md;
      isEditingRef.current = true;
      dirtyRef.current = restoredDirty;
      setMarkdown(md);
      editor.commands.setContent(html, { emitUpdate: true });
      setIsEditing(true);
      setDirty(restoredDirty);
    },
    [editor, suppressDirtyTrackingForTick],
  );

  const loadTree = useCallback(async (): Promise<PageTreeNode[]> => {
    try {
      const r = await fetch(`/api/spaces/${encodeURIComponent(space)}/pages`, { credentials: "include" });
      if (!r.ok) {
        throw new Error(await r.text());
      }
      const j = (await r.json()) as { tree?: PageTreeNode[] };
      const nextTree = mergePageAndFolderSiblings(Array.isArray(j.tree) ? j.tree : []);
      setTree(nextTree);
      return nextTree;
    } catch (e) {
      setError(e instanceof Error ? e.message : "failed to load page index");
      setTree([]);
      return [];
    }
  }, [space]);

  const loadComments = useCallback(async () => {
    try {
      const r = await fetch(`/api/spaces/${encodeURIComponent(space)}/comments?path=${encodeURIComponent(path)}`, {
        credentials: "include",
      });
      if (!r.ok) {
        throw new Error(await r.text());
      }
      const j = (await r.json()) as { comments?: CommentThread[] };
      const next: Record<string, CommentThread> = {};
      for (const c of j.comments ?? []) {
        if (typeof c.anchor_id === "string" && c.anchor_id.length > 0) {
          next[c.anchor_id] = c;
        }
      }
      setThreadsByAnchor(next);
    } catch {
      setThreadsByAnchor({});
    }
  }, [path, space]);

  const loadSettings = useCallback(async () => {
    try {
      const r = await fetch("/api/settings", { credentials: "include" });
      if (!r.ok) {
        throw new Error(await r.text());
      }
      const j = (await r.json()) as SettingsInfo;
      setSettingsInfo(j);
    } catch {
      setSettingsInfo(null);
    }
  }, []);

  const loadDraft = useCallback(async () => {
    try {
      const r = await fetch(`/api/spaces/${encodeURIComponent(space)}/draft?path=${encodeURIComponent(path)}`, {
        credentials: "include",
      });
      if (!r.ok) {
        throw new Error(await r.text());
      }
      const j = (await r.json()) as DraftInfo;
      setDraftInfo(j.exists ? j : null);
    } catch {
      setDraftInfo(null);
    }
  }, [path, space]);

  const persistDraft = useCallback(async () => {
    if (!editor || !isEditingRef.current || !dirtyRef.current) {
      return;
    }
    const md = htmlToMarkdown(editor.getHTML());
    const updateB64 = uint8ToBase64(Y.encodeStateAsUpdate(ydoc));
    const r = await fetch(`/api/spaces/${encodeURIComponent(space)}/draft`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "include",
      body: JSON.stringify({
        path,
        base_commit: baseCommit,
        format: "yjs",
        update_b64: updateB64,
        markdown: md,
      }),
    });
    if (!r.ok) {
      throw new Error(await readApiError(r, "draft save failed"));
    }
    const j = (await r.json()) as { updated_at?: string; base_commit?: string };
    setDraftInfo((prev) => ({
      exists: true,
      markdown: md,
      update_b64: updateB64,
      format: "yjs",
      updated_at: j.updated_at ?? prev?.updated_at,
      base_commit: j.base_commit ?? baseCommit,
      current_base_commit: prev?.current_base_commit ?? baseCommit,
      base_changed: prev?.base_changed ?? false,
    }));
  }, [baseCommit, editor, path, space, ydoc]);

  useEffect(() => {
    const flushDraft = () => {
      if (!dirtyRef.current || !isEditingRef.current) {
        return;
      }
      void persistDraft().catch(() => {});
    };
    const onVisibility = () => {
      if (document.hidden) {
        flushDraft();
      }
    };
    window.addEventListener("blur", flushDraft);
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      window.removeEventListener("blur", flushDraft);
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [persistDraft]);

  const discardDraft = useCallback(async () => {
    const r = await fetch(`/api/spaces/${encodeURIComponent(space)}/draft?path=${encodeURIComponent(path)}`, {
      method: "DELETE",
      credentials: "include",
    });
    if (!r.ok) {
      throw new Error(await readApiError(r, "draft delete failed"));
    }
    setDraftInfo(null);
  }, [path, space]);

  const refreshPageFromHttp = useCallback(async () => {
    const r = await fetch(`/api/spaces/${encodeURIComponent(space)}/page?path=${encodeURIComponent(path)}`, {
      credentials: "include",
    });
    if (!r.ok) {
      return;
    }
    const j = (await r.json()) as { content?: string; base_commit?: string };
    const md = typeof j.content === "string" ? j.content : DEFAULT_PAGE_TEXT;
    const normalized = canonicalMarkdown(md);
    setBaseCommit(typeof j.base_commit === "string" ? j.base_commit : "");
    if (editor) {
      await applyTrustedMarkdown(md);
    } else {
      markdownRef.current = md;
      lastSavedMarkdownRef.current = normalized;
      setMarkdown(md);
      setDirty(false);
    }
  }, [applyTrustedMarkdown, editor, path, space]);

  const refreshViewerFromLatest = useCallback(async () => {
    setError(null);
    try {
      await refreshPageFromHttp();
      await loadComments();
      setRefreshPrompt(null);
      setStatus(`Loaded latest saved version ${new Date().toLocaleTimeString()}`);
    } catch (e) {
      setError(e instanceof Error ? e.message : "failed to refresh page");
    }
  }, [loadComments, refreshPageFromHttp]);

  const checkForSavedPageUpdate = useCallback(async () => {
    if (isEditingRef.current || dirtyRef.current) {
      return;
    }
    try {
      const r = await fetch(`/api/spaces/${encodeURIComponent(space)}/page?path=${encodeURIComponent(path)}`, {
        credentials: "include",
      });
      if (!r.ok) {
        return;
      }
      const j = (await r.json()) as { content?: string; base_commit?: string };
      const nextCommit = typeof j.base_commit === "string" ? j.base_commit : "";
      const nextContent = typeof j.content === "string" ? j.content : DEFAULT_PAGE_TEXT;
      const savedMarkdown = canonicalMarkdown(lastSavedMarkdownRef.current);
      const incomingMarkdown = canonicalMarkdown(nextContent);
      const hasNewSavedVersion =
        (nextCommit !== "" && nextCommit !== baseCommit) || (nextCommit === "" && incomingMarkdown !== savedMarkdown);
      if (hasNewSavedVersion) {
        setRefreshPrompt({ path, commit: nextCommit });
      }
    } catch {
      // Keep showing the current saved snapshot if the background check fails.
    }
  }, [baseCommit, path, space]);

  useEffect(() => {
    void loadTree();
    void loadComments();
    void loadDraft();
  }, [loadComments, loadDraft, loadTree]);

  useEffect(() => {
    if (isEditing) {
      return;
    }
    void refreshPageFromHttp();
  }, [isEditing, refreshPageFromHttp]);

  useEffect(() => {
    if (!editor || isEditing) {
      return;
    }
    const desired = canonicalMarkdown(markdownRef.current);
    const current = canonicalMarkdown(htmlToMarkdown(editor.getHTML()));
    if (desired === current) {
      return;
    }
    void applyTrustedMarkdown(markdownRef.current);
  }, [applyTrustedMarkdown, editor, isEditing, path, space]);

  useEffect(() => {
    const onFocus = () => {
      void loadComments();
      void loadTree();
    };
    const onVisibility = () => {
      if (!document.hidden) {
        void loadComments();
        void loadTree();
      }
    };
    window.addEventListener("focus", onFocus);
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      window.removeEventListener("focus", onFocus);
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [loadComments, loadTree]);

  useEffect(() => {
    void loadSettings();
  }, [loadSettings]);

  useEffect(() => {
    const mode = settingsInfo?.settings?.save_mode;
    if (mode === "local" || mode === "git_sync") {
      setSettingsSaveMode(mode);
    }
  }, [settingsInfo]);

  useEffect(() => {
    if (!editor || !isEditing) {
      setReadOnly(false);
      setSyncMsg("");
      return;
    }
    let cancelled = false;
    let bootHandled = false;
    let fallbackBooted = false;
    let reconnectScheduled = false;
    let awaitingPeerSync = false;
    let httpSeededWhileAwaitingPeerSync = false;
    const shouldSeedFromHttp = () => ydoc.getXmlFragment("content").length === 0;
    const clearSharedContent = () => {
      const fragment = ydoc.getXmlFragment("content");
      if (fragment.length === 0) {
        return;
      }
      ydoc.transact(() => {
        fragment.delete(0, fragment.length);
      }, "remote");
    };
    const seedFromHttpIfEmpty = (options?: { awaitingPeerSyncFallback?: boolean }) => {
      if (cancelled || !shouldSeedFromHttp()) {
        return;
      }
      if (options?.awaitingPeerSyncFallback) {
        httpSeededWhileAwaitingPeerSync = true;
      }
      void refreshPageFromHttp();
    };

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
      wsReconnectAttemptsRef.current = 0;
      reconnectScheduled = false;
      setError((prev) => (prev === REALTIME_CONNECTION_FAILED_ERROR ? null : prev));
      setSyncMsg("");
      ws.send(JSON.stringify({ type: "need_sync" }));
    };

    ws.onmessage = (ev) => {
      if (ev.data instanceof ArrayBuffer) {
        if (awaitingPeerSync && httpSeededWhileAwaitingPeerSync) {
          clearSharedContent();
          httpSeededWhileAwaitingPeerSync = false;
        }
        awaitingPeerSync = false;
        suppressDirtyTrackingForTick();
        applyingRemoteSyncRef.current = true;
        Y.applyUpdate(ydoc, new Uint8Array(ev.data), "remote");
        window.setTimeout(() => {
          applyingRemoteSyncRef.current = false;
        }, 0);
        return;
      }

      let ctrl: { type?: string; reason?: string; for_client?: string; path?: string; commit?: string };
      try {
        ctrl = JSON.parse(String(ev.data));
      } catch {
        return;
      }

      if (ctrl.type === "request_state" && ctrl.for_client) {
        void sendStateBlob(ctrl.for_client);
        return;
      }
      if (ctrl.type === "need_sync") {
        awaitingPeerSync = true;
        return;
      }
      if (ctrl.type === "pages_invalidated") {
        void loadTree();
        return;
      }
      if (ctrl.type === "page_saved") {
        if (!isEditingRef.current && ctrl.path === path) {
          setRefreshPrompt({ path: ctrl.path, commit: ctrl.commit });
        }
        return;
      }

      if (!bootHandled) {
        if (ctrl.type === "sync_ok") {
          bootHandled = true;
          fallbackBooted = true;
          awaitingPeerSync = false;
          seedFromHttpIfEmpty();
        } else if (ctrl.type === "sync_lock") {
          bootHandled = true;
          fallbackBooted = true;
          awaitingPeerSync = true;
          setTimeout(() => {
            if (cancelled || ydoc.getXmlFragment("content").length > 0) {
              return;
            }
            seedFromHttpIfEmpty({ awaitingPeerSyncFallback: true });
          }, 8000);
        }
      }

      if (ctrl.type === "sync_lock") {
        setReadOnly(true);
        setSyncMsg(ctrl.reason ?? "locked until peer sync");
      } else if (ctrl.type === "sync_ok") {
        awaitingPeerSync = false;
        httpSeededWhileAwaitingPeerSync = false;
        setReadOnly(false);
        setSyncMsg("");
      }
    };

    const scheduleReconnect = () => {
      if (cancelled || reconnectScheduled) {
        return;
      }
      reconnectScheduled = true;
      const attempt = wsReconnectAttemptsRef.current;
      const delayMs = Math.min(10000, 1000 * Math.pow(2, attempt));
      wsReconnectAttemptsRef.current = attempt + 1;
      setSyncMsg(`Realtime disconnected. Reconnecting in ${Math.ceil(delayMs / 1000)}s…`);
      wsReconnectTimerRef.current = window.setTimeout(() => {
        wsReconnectTimerRef.current = null;
        if (cancelled) {
          return;
        }
        setWsReconnectTick((n) => n + 1);
      }, delayMs);
    };

    const onDisconnect = () => {
      if (cancelled || reconnectScheduled) {
        return;
      }
      if (!fallbackBooted) {
        fallbackBooted = true;
        seedFromHttpIfEmpty();
      }
      // Refresh sidebar/page snapshot only on the first disconnect in a reconnect streak.
      // Repeated retries should not continuously hit HTTP endpoints.
      if (wsReconnectAttemptsRef.current === 0) {
        void loadTree();
        void loadComments();
        if (!dirtyRef.current) {
          seedFromHttpIfEmpty();
        }
      }
      scheduleReconnect();
    };

    ws.onerror = () => {
      onDisconnect();
    };

    ws.onclose = () => {
      onDisconnect();
    };

    const fallbackTimer = window.setTimeout(() => {
      if (!bootHandled && !fallbackBooted && !awaitingPeerSync) {
        fallbackBooted = true;
        seedFromHttpIfEmpty();
      }
    }, 1200);

    return () => {
      cancelled = true;
      window.clearTimeout(fallbackTimer);
      if (wsReconnectTimerRef.current) {
        window.clearTimeout(wsReconnectTimerRef.current);
        wsReconnectTimerRef.current = null;
      }
      if (dirtyTrackingResumeTimerRef.current) {
        window.clearTimeout(dirtyTrackingResumeTimerRef.current);
        dirtyTrackingResumeTimerRef.current = null;
      }
      ydoc.off("update", updateHandler);
      ws.close();
    };
  }, [editor, isEditing, loadComments, loadTree, path, refreshPageFromHttp, space, suppressDirtyTrackingForTick, wsReconnectTick, ydoc]);

  useEffect(() => {
    if (isEditing) {
      return;
    }
    const wsUrl = `${location.protocol === "https:" ? "wss:" : "ws:"}//${location.host}/ws?watch=1&space=${encodeURIComponent(space)}`;
    const ws = new WebSocket(wsUrl);
    ws.onmessage = (ev) => {
      if (ev.data instanceof ArrayBuffer) {
        return;
      }
      let ctrl: { type?: string; path?: string; commit?: string };
      try {
        ctrl = JSON.parse(String(ev.data));
      } catch {
        return;
      }
      if (ctrl.type === "pages_invalidated") {
        void loadTree().then((nextTree) => {
          if (!treeHasPagePath(nextTree, path)) {
            setRefreshPrompt({ path, commit: ctrl.commit });
          }
        });
        return;
      }
      if (ctrl.type === "page_saved" && ctrl.path === path) {
        setRefreshPrompt({ path: ctrl.path, commit: ctrl.commit });
      }
    };
    const onFocus = () => {
      void checkForSavedPageUpdate();
    };
    const onVisibility = () => {
      if (!document.hidden) {
        void checkForSavedPageUpdate();
      }
    };
    window.addEventListener("focus", onFocus);
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      window.removeEventListener("focus", onFocus);
      document.removeEventListener("visibilitychange", onVisibility);
      ws.close();
    };
  }, [checkForSavedPageUpdate, isEditing, loadTree, path, space]);

  const save = useCallback(async () => {
    if (!dirty) {
      return;
    }
    setSaving(true);
    setError(null);
    setStatus("");
    try {
      const result = await commitCurrentState();
      const normalized = normalizeApiErrorMessage(result.message);
      if (result.mode === "queued") {
        setAsyncSaveState("queued");
        setStatus("Syncing…");
        setError(null);
        setConsecutiveSaveFailures(0);
        setDirty(false);
      } else if (normalized.toLowerCase().includes("push failed")) {
        setError(normalized);
        setStatus(`Saved locally ${new Date().toLocaleTimeString()}`);
      } else {
        setStatus(`Saved ${new Date().toLocaleTimeString()}`);
        setAsyncSaveState("idle");
      }
      if (result.mode !== "queued") {
        setConsecutiveSaveFailures(0);
        setDirty(false);
        await discardDraft().catch(() => {});
        setDraftInfo(null);
        await loadTree();
        await loadComments();
      }
    } catch (e) {
      const nextErr = e instanceof Error ? e.message : "save failed";
      setError(nextErr);
      const nextFailures = consecutiveSaveFailures + 1;
      setConsecutiveSaveFailures(nextFailures);
      if (nextFailures >= 3) {
        setStatus("Autosave paused after 3 failures. Fix the issue, then click Save.");
      }
    } finally {
      setSaving(false);
    }
  }, [commitCurrentState, consecutiveSaveFailures, discardDraft, loadComments, loadTree]);

  useEffect(() => {
    const onSaveShortcut = (e: KeyboardEvent) => {
      const isSaveKey = e.key.toLowerCase() === "s";
      if (!isSaveKey || (!e.metaKey && !e.ctrlKey)) {
        return;
      }
      if (!isEditingRef.current || readOnly || saving || !dirtyRef.current) {
        return;
      }
      e.preventDefault();
      void save();
    };
    window.addEventListener("keydown", onSaveShortcut);
    return () => {
      window.removeEventListener("keydown", onSaveShortcut);
    };
  }, [readOnly, save, saving]);

  useEffect(() => {
    setDirty(false);
    setIsEditing(false);
    markdownRef.current = DEFAULT_PAGE_TEXT;
    lastSavedMarkdownRef.current = "";
    setDraftInfo(null);
    setBaseCommit("");
    setPendingNavigation(null);
    setNavigationDecisionBusy(null);
    setRefreshPrompt(null);
    setAsyncSaveState(currentPagePendingSave?.status === "running" ? "running" : currentPagePendingSave?.status === "queued" ? "queued" : currentPagePendingSave?.status === "failed" ? "failed" : "idle");
  }, [currentPagePendingSave?.status, path, space]);

  useEffect(() => {
    if (!currentPagePendingSave || currentPagePendingSave.space !== space || currentPagePendingSave.path !== path) {
      if (asyncSaveState !== "idle") {
        setAsyncSaveState("idle");
      }
      return;
    }
    if (currentPagePendingSave.status === "queued") {
      setAsyncSaveState("queued");
      setStatus("Syncing…");
      setError(null);
      return;
    }
    if (currentPagePendingSave.status === "running") {
      setAsyncSaveState("running");
      setStatus("Syncing to git…");
      setError(null);
      return;
    }
    if (currentPagePendingSave.status === "succeeded") {
      setAsyncSaveState("idle");
      setError(null);
      setBaseCommit(currentPagePendingSave.commit ?? "");
      setStatus(`Saved ${new Date().toLocaleTimeString()}`);
      setConsecutiveSaveFailures(0);
      return;
    }
    if (currentPagePendingSave.status === "failed") {
      setAsyncSaveState("failed");
      setError(currentPagePendingSave.error || currentPagePendingSave.message || "save sync failed");
      setStatus("Save queued but failed during sync");
      if (currentPagePendingSave.snapshot && canonicalMarkdown(markdownRef.current) === canonicalMarkdown(currentPagePendingSave.snapshot)) {
        setDirty(true);
      }
    }
  }, [asyncSaveState, currentPagePendingSave, path, space]);

  useEffect(() => {
    if (!canEdit) {
      return;
    }
    const id = window.setInterval(() => {
      if (!dirty || saving) {
        return;
      }
      void persistDraft().catch(() => {});
    }, 5000);
    return () => {
      window.clearInterval(id);
    };
  }, [canEdit, dirty, persistDraft, saving]);

  const toggleEditing = useCallback(async () => {
    if (isEditing) {
      if (dirtyRef.current) {
        if (saving) {
          return;
        }
        if (consecutiveSaveFailures < 3) {
          await save();
          if (dirtyRef.current) {
            return;
          }
        } else if (!window.confirm("You have unsaved changes on this page. Leave edit mode anyway?")) {
          return;
        }
      }
      setIsEditing(false);
      return;
    }
    setRefreshPrompt(null);
    setEditorSession((n) => n + 1);
    setIsEditing(true);
  }, [consecutiveSaveFailures, isEditing, save, saving]);

  const cancelPendingNavigation = useCallback(() => {
    if (navigationDecisionBusy) {
      return;
    }
    setPendingNavigation(null);
  }, [navigationDecisionBusy]);

  const saveAndContinueNavigation = useCallback(async () => {
    if (!pendingNavigation || navigationDecisionBusy) {
      return;
    }
    setNavigationDecisionBusy("save");
    try {
      await save();
      if (dirtyRef.current) {
        return;
      }
      applyPendingNavigation(pendingNavigation);
    } finally {
      setNavigationDecisionBusy(null);
    }
  }, [applyPendingNavigation, navigationDecisionBusy, pendingNavigation, save]);

  const discardAndContinueNavigation = useCallback(async () => {
    if (!pendingNavigation || navigationDecisionBusy) {
      return;
    }
    setNavigationDecisionBusy("discard");
    try {
      await discardDraft().catch(() => {});
      setDraftInfo(null);
      applyPendingNavigation(pendingNavigation);
    } finally {
      setNavigationDecisionBusy(null);
    }
  }, [applyPendingNavigation, discardDraft, navigationDecisionBusy, pendingNavigation]);

  const selectedSpace = useMemo(() => spaces.find((s) => s.key === space) ?? null, [space, spaces]);
  const isSpaceCreator = useMemo(() => {
    const creator = (selectedSpace?.created_by_login ?? "").trim().toLowerCase();
    const login = (currentUserLogin ?? "").trim().toLowerCase();
    return creator !== "" && login !== "" && creator === login;
  }, [currentUserLogin, selectedSpace?.created_by_login]);
  const createParentOptions = useMemo<CreateParentOption[]>(() => {
    const options: CreateParentOption[] = [
      { value: "current", label: `Current page: ${pageTitle(path)}`, prefix: pageParentPrefix(path) },
      { value: "space", label: `Space: ${selectedSpace?.display_name || space}`, prefix: "" },
    ];
    for (const node of tree) {
      if (node.type !== "page" || node.path === path) {
        continue;
      }
      options.push({
        value: `page:${node.path}`,
        label: `Page: ${pageTitle(node.path)}`,
        prefix: pageParentPrefix(node.path),
      });
    }
    return options;
  }, [path, selectedSpace?.display_name, space, tree]);
  const createParentPrefix = useMemo(
    () => createParentOptions.find((opt) => opt.value === createParent)?.prefix ?? pageParentPrefix(path),
    [createParent, createParentOptions, path],
  );
  const createPageSuggestion = useMemo(() => {
    const base = createPageInput.trim() || "new-page.md";
    const normalized = base.replace(/^\/+/, "");
    return createParentPrefix ? `${createParentPrefix}/${normalized}` : normalized;
  }, [createPageInput, createParentPrefix]);

  useEffect(() => {
    if (!createPageOpen) {
      return;
    }
    setCreateParent("current");
    setCreatePageInput("new-page.md");
  }, [createPageOpen, path, space]);

  useEffect(() => {
    if (!pageContextMenu && !spaceContextMenu) {
      return;
    }
    const closeMenus = () => {
      setPageContextMenu(null);
      setSpaceContextMenu(null);
    };
    const onPointerDown = (e: PointerEvent) => {
      if (e.button !== 0) {
        return;
      }
      const target = e.target as HTMLElement | null;
      if (target?.closest(".editor-context-menu")) {
        return;
      }
      closeMenus();
    };
    const onWindowContextMenu = (e: MouseEvent) => {
      const target = e.target as HTMLElement | null;
      if (target?.closest(".tree-page-row") || target?.closest(".space-selector") || target?.closest(".editor-context-menu")) {
        return;
      }
      closeMenus();
    };
    const onEsc = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        closeMenus();
      }
    };
    window.addEventListener("pointerdown", onPointerDown);
    window.addEventListener("contextmenu", onWindowContextMenu);
    window.addEventListener("resize", closeMenus);
    window.addEventListener("scroll", closeMenus, true);
    window.addEventListener("keydown", onEsc);
    return () => {
      window.removeEventListener("pointerdown", onPointerDown);
      window.removeEventListener("contextmenu", onWindowContextMenu);
      window.removeEventListener("resize", closeMenus);
      window.removeEventListener("scroll", closeMenus, true);
      window.removeEventListener("keydown", onEsc);
    };
  }, [pageContextMenu, spaceContextMenu]);

  const saveSettings = useCallback(async () => {
    setSettingsSaving(true);
    try {
      const r = await fetch("/api/settings", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ save_mode: settingsSaveMode }),
      });
      if (!r.ok) {
        throw new Error(await readApiError(r, "failed to save settings"));
      }
      await loadSettings();
      setStatus(`Settings updated ${new Date().toLocaleTimeString()}`);
    } catch (e) {
      setError(e instanceof Error ? e.message : "failed to save settings");
    } finally {
      setSettingsSaving(false);
    }
  }, [loadSettings, settingsSaveMode]);

  const createPage = useCallback(async () => {
    const nextPathInput = createPageInput.trim();
    if (!nextPathInput) {
      return;
    }
    const normalizedInput = nextPathInput.replace(/^\/+/, "");
    const finalPath = createParentPrefix ? `${createParentPrefix}/${normalizedInput}` : normalizedInput;
    try {
      setError(null);
      const r = await fetch(`/api/spaces/${encodeURIComponent(space)}/pages`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({ path: finalPath }),
      });
      if (!r.ok) {
        throw new Error(await readApiError(r, "failed to create page"));
      }
      const j = (await r.json()) as { path?: string; content?: string };
      if (j.path) {
        onPathChange(j.path);
      }
      if (typeof j.content === "string") {
        await applyTrustedMarkdown(j.content);
      }
      setCreatePageOpen(false);
      await loadTree();
      await loadComments();
    } catch (e) {
      setError(e instanceof Error ? e.message : "failed to create page");
    }
  }, [createPageInput, createParentPrefix, editor, loadComments, loadTree, onPathChange, space]);

  const createSpace = useCallback(async () => {
    const displayNameInput = window.prompt("New space name", "New Space");
    if (!displayNameInput) {
      return;
    }
    const displayName = displayNameInput.trim();
    if (!displayName) {
      return;
    }
    const key = pickUniqueSpaceKey(makeSpaceKey(displayName), spaces);
    try {
      setError(null);
      const r = await fetch("/api/spaces", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ key, display_name: displayName }),
      });
      if (!r.ok) {
        throw new Error(await readApiError(r, "failed to create space"));
      }
      const j = (await r.json()) as { space?: { key?: string } };
      const createdKey = j.space?.key ?? key;
      await onSpacesChanged();
      onSpaceChange(createdKey);
      onPathChange("README.md");
      setStatus(`Space created ${new Date().toLocaleTimeString()}`);
    } catch (e) {
      setError(e instanceof Error ? e.message : "failed to create space");
    }
  }, [onPathChange, onSpaceChange, onSpacesChanged, spaces]);

  const renameSpace = useCallback(async () => {
    if (!selectedSpace) {
      return;
    }
    if (!isSpaceCreator) {
      setError("Only the space creator can rename this space.");
      return;
    }
    const nextNameInput = window.prompt("Rename space to", selectedSpace.display_name || selectedSpace.key);
    if (!nextNameInput) {
      return;
    }
    const nextName = nextNameInput.trim();
    if (!nextName || nextName === selectedSpace.display_name) {
      return;
    }
    try {
      setError(null);
      const r = await fetch(`/api/spaces/${encodeURIComponent(selectedSpace.key)}/rename`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ display_name: nextName }),
      });
      if (!r.ok) {
        throw new Error(await readApiError(r, "failed to rename space"));
      }
      await onSpacesChanged();
      setStatus(`Space renamed ${new Date().toLocaleTimeString()}`);
    } catch (e) {
      setError(e instanceof Error ? e.message : "failed to rename space");
    }
  }, [isSpaceCreator, onSpacesChanged, selectedSpace]);

  const deleteSpace = useCallback(async () => {
    if (!selectedSpace) {
      return;
    }
    if (!isSpaceCreator) {
      setError("Only the space creator can delete this space.");
      return;
    }
    const confirmed = window.confirm(
      `Delete space "${selectedSpace.display_name || selectedSpace.key}"? This removes the space from the workspace.`,
    );
    if (!confirmed) {
      return;
    }
    try {
      setError(null);
      const r = await fetch(`/api/spaces/${encodeURIComponent(selectedSpace.key)}`, {
        method: "DELETE",
        credentials: "include",
      });
      if (!r.ok) {
        throw new Error(await readApiError(r, "failed to delete space"));
      }
      await onSpacesChanged();
      const fallback = spaces.find((s) => s.key !== selectedSpace.key)?.key ?? "main";
      onSpaceChange(fallback);
      onPathChange("README.md");
      setStatus(`Space deleted ${new Date().toLocaleTimeString()}`);
    } catch (e) {
      setError(e instanceof Error ? e.message : "failed to delete space");
    }
  }, [isSpaceCreator, onPathChange, onSpaceChange, onSpacesChanged, selectedSpace, spaces]);

  const renamePage = useCallback(async () => {
    const suggestion = path;
    const nextPathInput = window.prompt("Rename page to (relative .md path)", suggestion);
    if (!nextPathInput) {
      return;
    }
    const nextPath = nextPathInput.trim();
    if (!nextPath || nextPath === path) {
      return;
    }
    try {
      setError(null);
      if (dirty) {
        await commitCurrentState();
      }
      const r = await fetch(`/api/spaces/${encodeURIComponent(space)}/pages/rename`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ from: path, to: nextPath }),
      });
      if (!r.ok) {
        throw new Error(await readApiError(r, "failed to rename page"));
      }
      const j = (await r.json()) as { path?: string };
      const renamedPath = j.path ?? nextPath;
      onPathChange(renamedPath);
      await loadTree();
      await loadComments();
      setStatus(`Page renamed ${new Date().toLocaleTimeString()}`);
      setConsecutiveSaveFailures(0);
    } catch (e) {
      setError(e instanceof Error ? e.message : "failed to rename page");
    }
  }, [commitCurrentState, dirty, loadComments, loadTree, onPathChange, path, space]);

  const deletePage = useCallback(
    async (pagePath: string) => {
      const confirmed = window.confirm(`Delete page "${pageTitle(pagePath)}"? This cannot be undone.`);
      if (!confirmed) {
        setPageContextMenu(null);
        return;
      }
      try {
        setError(null);
        const r = await fetch(
          `/api/spaces/${encodeURIComponent(space)}/pages?path=${encodeURIComponent(pagePath)}`,
          { method: "DELETE", credentials: "include" },
        );
        if (!r.ok) {
          throw new Error(await readApiError(r, "failed to delete page"));
        }
        const nextTree = await loadTree();
        if (pagePath === path) {
          const fallbackPath = firstPagePath(nextTree);
          onPathChange(fallbackPath ?? "README.md");
        } else {
          await loadComments();
        }
        setStatus(`Page deleted ${new Date().toLocaleTimeString()}`);
        setConsecutiveSaveFailures(0);
      } catch (e) {
        setError(e instanceof Error ? e.message : "failed to delete page");
      } finally {
        setPageContextMenu(null);
      }
    },
    [loadComments, loadTree, onPathChange, path, space],
  );

  const renamePageAtPath = useCallback(
    async (fromPath: string) => {
      const nextPathInput = window.prompt("Rename page to (relative .md path)", fromPath);
      if (!nextPathInput) {
        setPageContextMenu(null);
        return;
      }
      const nextPath = nextPathInput.trim();
      if (!nextPath || nextPath === fromPath) {
        setPageContextMenu(null);
        return;
      }
      try {
        setError(null);
        if (dirty && fromPath === path) {
          await commitCurrentState();
        }
        const r = await fetch(`/api/spaces/${encodeURIComponent(space)}/pages/rename`, {
          method: "POST",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ from: fromPath, to: nextPath }),
        });
        if (!r.ok) {
          throw new Error(await readApiError(r, "failed to rename page"));
        }
        const j = (await r.json()) as { path?: string };
        const renamedPath = j.path ?? nextPath;
        if (fromPath === path) {
          onPathChange(renamedPath);
        } else {
          await loadComments();
        }
        await loadTree();
        setStatus(`Page renamed ${new Date().toLocaleTimeString()}`);
        setConsecutiveSaveFailures(0);
      } catch (e) {
        setError(e instanceof Error ? e.message : "failed to rename page");
      } finally {
        setPageContextMenu(null);
      }
    },
    [commitCurrentState, dirty, loadComments, loadTree, onPathChange, path, space],
  );

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
    if (!canComment) {
      setError("Comments are unavailable while realtime sync is read-only.");
      setContextMenu(null);
      return;
    }
    if (contextMenu.from >= contextMenu.to) {
      setError("Select text first, then right-click to add a comment.");
      setContextMenu(null);
      return;
    }
    const comment = window.prompt("Comment");
    if (!comment || comment.trim().length === 0) {
      setContextMenu(null);
      return;
    }
    const anchorID = `a_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`;
    const selected = editor.state.doc.textBetween(contextMenu.from, contextMenu.to, "\n");
    const selectedHTML = escapeHTML(selected);
    editor
      .chain()
      .focus()
      .insertContentAt(
        { from: contextMenu.from, to: contextMenu.to },
        `<mark data-wiki-comment="${anchorID}" class="wiki-comment-highlight wiki-comment-id-${anchorID}">${selectedHTML}</mark>`,
      )
      .run();
    try {
      const r = await fetch(`/api/spaces/${encodeURIComponent(space)}/comments`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          path,
          anchor_id: anchorID,
          comment: comment.trim(),
          position: contextMenu.from,
        }),
      });
      if (!r.ok) {
        throw new Error(await r.text());
      }
      await commitCurrentState();
      setStatus(`Comment added ${new Date().toLocaleTimeString()}`);
      await loadComments();
    } catch (e) {
      setError(e instanceof Error ? e.message : "failed to add comment");
    } finally {
      setContextMenu(null);
      await loadTree();
    }
  }, [canComment, commitCurrentState, contextMenu, editor, loadComments, loadTree, path, space]);

  const withThreadMutation = useCallback(
    async (
      fn: () => Promise<Response>,
      okStatus: string,
      applyLocal?: (json: Record<string, unknown>) => void,
    ) => {
      try {
        const r = await fn();
        if (!r.ok) {
          throw new Error(await r.text());
        }
        const j = (await r.json()) as Record<string, unknown>;
        if (applyLocal) {
          applyLocal(j);
        }
        await commitCurrentState();
        await loadComments();
        setStatus(okStatus);
      } catch (e) {
        setError(e instanceof Error ? e.message : "comment update failed");
      }
    },
    [commitCurrentState, loadComments],
  );

  const restoreDraft = useCallback(async () => {
    if (!draftInfo?.markdown) {
      return;
    }
    await applyRecoveredMarkdown(draftInfo.markdown);
    setStatus(`Recovered draft ${new Date().toLocaleTimeString()}`);
  }, [applyRecoveredMarkdown, draftInfo?.markdown]);

  const shouldShowDraftBanner = Boolean(
    draftInfo?.exists &&
      !isEditing &&
      canonicalMarkdown(draftInfo.markdown ?? "") !== canonicalMarkdown(lastSavedMarkdownRef.current),
  );
  const draftBannerInfo = shouldShowDraftBanner ? draftInfo : null;

  const triggerImageUpload = useCallback(() => {
    imageInputRef.current?.click();
  }, []);

  const uploadImageAndInsert = useCallback(
    async (file: File) => {
      if (!editor) {
        return;
      }
      const fd = new FormData();
      fd.append("file", file);
      const r = await fetch(`/api/spaces/${encodeURIComponent(space)}/assets/image`, {
        method: "POST",
        credentials: "include",
        body: fd,
      });
      if (!r.ok) {
        throw new Error(await readApiError(r, "image upload failed"));
      }
      const j = (await r.json()) as { path?: string };
      const imagePath = typeof j.path === "string" ? j.path : "";
      if (!imagePath) {
        throw new Error("image upload did not return a path");
      }
      editor.chain().focus().setImage({ src: relativizeAssetPath(path, imagePath), alt: file.name }).run();
      setDirty(true);
      setStatus(`Image inserted ${new Date().toLocaleTimeString()}`);
      await loadTree();
    },
    [editor, loadTree, path, space],
  );

  const createDiagramAsset = useCallback(
    async (kind: "excalidraw" | "drawio") => {
      if (!editor) {
        return;
      }
      const suggested = kind === "excalidraw" ? "diagram.excalidraw" : "diagram.drawio";
      const name = window.prompt(`New ${kind} file name`, suggested);
      if (!name || name.trim().length === 0) {
        return;
      }
      const r = await fetch(`/api/spaces/${encodeURIComponent(space)}/diagrams`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ kind, name: name.trim() }),
      });
      if (!r.ok) {
        throw new Error(await readApiError(r, "diagram create failed"));
      }
      const j = (await r.json()) as { path?: string; content?: string };
      const diagramPath = typeof j.path === "string" ? j.path : "";
      if (!diagramPath) {
        throw new Error("diagram create did not return a path");
      }
      editor.chain().focus().insertContent(diagramPlaceholderHTML(relativizeAssetPath(path, diagramPath))).run();
      setDirty(true);
      setDiagramEditor({
        path: resolveAssetPath(path, diagramPath),
        kind,
        content: typeof j.content === "string" ? j.content : "",
      });
      setStatus(`Diagram created ${new Date().toLocaleTimeString()}`);
      await loadTree();
    },
    [editor, loadTree, path, space],
  );

  const openDiagramEditor = useCallback(
    async (diagramPath: string) => {
      const resolvedPath = resolveAssetPath(path, diagramPath);
      const kind = diagramKindForPath(resolvedPath);
      if (!kind) {
        return;
      }
      const r = await fetch(`/api/spaces/${encodeURIComponent(space)}/diagram?path=${encodeURIComponent(resolvedPath)}`, {
        credentials: "include",
      });
      if (!r.ok) {
        throw new Error(await readApiError(r, "diagram load failed"));
      }
      const j = (await r.json()) as { content?: string };
      setDiagramEditor({
        path: resolvedPath,
        kind,
        content: typeof j.content === "string" ? j.content : "",
      });
    },
    [path, space],
  );

  const saveDiagramEditor = useCallback(async () => {
    if (!diagramEditor) {
      return;
    }
    const r = await fetch(`/api/spaces/${encodeURIComponent(space)}/diagram`, {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        path: diagramEditor.path,
        content: diagramEditor.content,
      }),
    });
    if (!r.ok) {
      throw new Error(await readApiError(r, "diagram save failed"));
    }
    setStatus(`Diagram saved ${new Date().toLocaleTimeString()}`);
    setDiagramEditor(null);
    await loadTree();
  }, [diagramEditor, loadTree, space]);

  const scheduleHidePopover = useCallback(() => {
    if (hideTimerRef.current) {
      clearTimeout(hideTimerRef.current);
    }
    hideTimerRef.current = setTimeout(() => {
      if (!popoverHoverRef.current) {
        setPopover(null);
      }
    }, 180);
  }, []);

  useEffect(() => {
    if (!editor) {
      return;
    }
    const timer = window.setTimeout(() => {
      const root = editor.view.dom as HTMLElement;
      root.querySelectorAll("img").forEach((img) => {
        const current = img.getAttribute("src") || "";
        const original = (img.getAttribute("data-mdwiki-src") || current).trim();
        if (!isRelativeAssetPath(original)) {
          return;
        }
        const resolvedPath = resolveAssetPath(path, original);
        img.setAttribute("data-mdwiki-src", original);
        img.setAttribute("src", assetApiURL(space, resolvedPath));
        img.classList.add("wiki-embedded-image");
      });
      root.querySelectorAll("a[href]").forEach((anchor) => {
        const href = (anchor.getAttribute("href") || "").trim();
        if (!href) {
          return;
        }
        const kind = diagramKindForPath(href);
        if (kind) {
          const host = anchor.closest("p") ?? anchor.parentElement ?? anchor;
          const name = anchor.textContent?.replace(/^Diagram:\s*/i, "").trim() || href.split("/").pop() || href;
          host.classList.add("diagram-embed-host");
          host.innerHTML = `
            <div class="diagram-embed-card" contenteditable="false">
              <div class="diagram-embed-meta">
                <strong>${escapeHTML(name)}</strong>
                <span>${escapeHTML(kind)}</span>
              </div>
              <div class="diagram-embed-actions">
                <button type="button" data-diagram-action="edit" data-diagram-path="${escapeHTML(href)}">Edit</button>
                <button type="button" data-diagram-action="open" data-diagram-path="${escapeHTML(href)}">Open file</button>
              </div>
            </div>`;
          return;
        }
        if (isRelativeAssetPath(href)) {
          const resolvedPath = resolveAssetPath(path, href);
          anchor.setAttribute("target", "_blank");
          anchor.setAttribute("rel", "noreferrer noopener");
          anchor.setAttribute("href", assetApiURL(space, resolvedPath));
        }
      });
    }, 60);
    return () => window.clearTimeout(timer);
  }, [editor, isEditing, markdown, space]);

  const activeThread = popover ? threadsByAnchor[popover.anchorId] : undefined;
  const headingValue = editor?.isActive("heading", { level: 1 })
    ? "h1"
    : editor?.isActive("heading", { level: 2 })
      ? "h2"
      : editor?.isActive("heading", { level: 3 })
        ? "h3"
        : "paragraph";

  const formatValue = editor?.isActive("bulletList")
    ? "bullet"
    : editor?.isActive("orderedList")
      ? "ordered"
      : editor?.isActive("blockquote")
        ? "quote"
        : editor?.isActive("codeBlock")
          ? "codeblock"
          : "normal";

  const applyHeading = (value: string) => {
    if (!editor) {
      return;
    }
    const chain = editor.chain().focus();
    if (value === "paragraph") {
      chain.setParagraph().run();
      return;
    }
    if (value === "h1") {
      chain.toggleHeading({ level: 1 }).run();
      return;
    }
    if (value === "h2") {
      chain.toggleHeading({ level: 2 }).run();
      return;
    }
    if (value === "h3") {
      chain.toggleHeading({ level: 3 }).run();
    }
  };

  const applyFormat = (value: string) => {
    if (!editor) {
      return;
    }
    const chain = editor.chain().focus();
    if (value === "normal") {
      chain.clearNodes().unsetAllMarks().run();
      return;
    }
    if (value === "bullet") {
      chain.toggleBulletList().run();
      return;
    }
    if (value === "ordered") {
      chain.toggleOrderedList().run();
      return;
    }
    if (value === "quote") {
      chain.toggleBlockquote().run();
      return;
    }
    if (value === "codeblock") {
      chain.toggleCodeBlock().run();
      return;
    }
    if (value === "inlinecode") {
      chain.toggleCode().run();
    }
  };

  const findCodeBlockAtDOM = useCallback(
    (pre: HTMLElement): { pos: number; language: string } | null => {
      if (!editor) {
        return null;
      }
      let domPos = 0;
      try {
        domPos = editor.view.posAtDOM(pre, 0);
      } catch {
        return null;
      }
      const docSize = editor.state.doc.content.size;
      const bounded = Math.max(0, Math.min(domPos, docSize));
      const resolved = editor.state.doc.resolve(bounded);
      for (let d = resolved.depth; d > 0; d--) {
        const node = resolved.node(d);
        if (node.type.name === "codeBlock") {
          return {
            pos: resolved.before(d),
            language: (node.attrs.language as string) || getLanguageFromCodeElement(pre),
          };
        }
      }
      for (const c of [bounded, bounded - 1, bounded + 1]) {
        if (c < 0 || c > docSize) {
          continue;
        }
        const node = editor.state.doc.nodeAt(c);
        if (node?.type.name === "codeBlock") {
          return {
            pos: c,
            language: (node.attrs.language as string) || getLanguageFromCodeElement(pre),
          };
        }
      }
      return null;
    },
    [editor],
  );

  const saveButtonState: "save" | "saving" | "saved" = saving ? "saving" : dirty ? "save" : "saved";

  return (
    <div className="wiki-shell">
      <header className="wiki-topbar">
        <div className="brand">mdwiki</div>
        <label
          className="space-selector"
          title="Right click for space actions"
          onContextMenu={(e) => {
            e.preventDefault();
            e.stopPropagation();
            setPageContextMenu(null);
            setSpaceContextMenu({ x: e.clientX, y: e.clientY });
          }}
        >
          Space
          <select value={space} onChange={(e) => navigateToSpace(e.target.value)}>
            {spaces.map((s) => (
              <option key={s.key} value={s.key}>
                {s.display_name || s.key}
              </option>
            ))}
          </select>
        </label>
        <button
          type="button"
          className="plus-btn"
          onClick={() => {
            setCreatePageOpen(true);
          }}
          title="Create page"
        >
          +
        </button>
        <button type="button" className="active-page-btn" onClick={() => void renamePage()} title="Rename page">
          {pageTitle(path)}
        </button>
        <div className="spacer" />
        <span className={`mode-badge ${isEditing ? "is-editing" : "is-viewing"}`}>{isEditing ? "Editing" : "Viewing"}</span>
        {readOnly ? <span className="sync-badge">Read-only: {syncMsg}</span> : null}
        <button
          type="button"
          className={`top-icon-btn mode-toggle-btn ${isEditing ? "is-active" : ""}`}
          title={isEditing ? "Stop editing" : "Edit page"}
          aria-label={isEditing ? "Stop editing" : "Edit page"}
          onClick={toggleEditing}
          disabled={readOnly}
        >
          <svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true">
            <path
              d="M3 17.25V21h3.75L17.8 9.94l-3.75-3.75L3 17.25Zm14.71-9.04a1.003 1.003 0 0 0 0-1.42l-2.5-2.5a1.003 1.003 0 0 0-1.42 0l-1.17 1.17 3.75 3.75 1.34-1Z"
              fill="currentColor"
            />
          </svg>
        </button>
        <button type="button" className="top-icon-btn" title="Settings" onClick={() => setSettingsOpen(true)}>
          <svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true">
            <path
              d="M19.14 12.94a7.8 7.8 0 0 0 .05-.94 7.8 7.8 0 0 0-.05-.94l2.03-1.58a.5.5 0 0 0 .12-.64l-1.92-3.32a.5.5 0 0 0-.6-.22l-2.39.96a7.12 7.12 0 0 0-1.63-.94l-.36-2.54A.5.5 0 0 0 13.9 2h-3.8a.5.5 0 0 0-.49.42l-.36 2.54c-.58.22-1.13.53-1.63.94l-2.39-.96a.5.5 0 0 0-.6.22L2.71 8.48a.5.5 0 0 0 .12.64l2.03 1.58a7.8 7.8 0 0 0-.05.94 7.8 7.8 0 0 0 .05.94L2.83 14.16a.5.5 0 0 0-.12.64l1.92 3.32c.13.22.39.31.6.22l2.39-.96c.5.4 1.05.72 1.63.94l.36 2.54c.04.24.24.42.49.42h3.8c.25 0 .45-.18.49-.42l.36-2.54c.58-.22 1.13-.53 1.63-.94l2.39.96c.22.09.47 0 .6-.22l1.92-3.32a.5.5 0 0 0-.12-.64l-2.03-1.58ZM12 15.2A3.2 3.2 0 1 1 12 8.8a3.2 3.2 0 0 1 0 6.4Z"
              fill="currentColor"
            />
          </svg>
        </button>
        <button type="button" className="top-icon-btn" title="Toggle theme" onClick={onToggleTheme}>
          {theme === "dark" ? (
            <svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true">
              <path
                d="M12 4.5a1 1 0 0 1 1 1v1.75a1 1 0 1 1-2 0V5.5a1 1 0 0 1 1-1Zm0 11.75a1 1 0 0 1 1 1V19a1 1 0 1 1-2 0v-1.75a1 1 0 0 1 1-1Zm7.5-5.25a1 1 0 0 1 1 1 1 1 0 0 1-1 1h-1.75a1 1 0 1 1 0-2H19.5ZM7.25 12a1 1 0 1 1 0 2H5.5a1 1 0 1 1 0-2h1.75Zm8.05-4.8a1 1 0 0 1 1.41 0l1.24 1.24a1 1 0 1 1-1.42 1.41L15.3 8.6a1 1 0 0 1 0-1.41Zm-8.24 8.24a1 1 0 0 1 1.41 0l1.24 1.24a1 1 0 1 1-1.41 1.42l-1.24-1.25a1 1 0 0 1 0-1.41Zm10.89.59a1 1 0 0 1 0 1.41l-1.24 1.25a1 1 0 1 1-1.41-1.42l1.24-1.24a1 1 0 0 1 1.41 0ZM9.71 8.6a1 1 0 1 1-1.41 1.41L7.06 8.77A1 1 0 0 1 8.47 7.36L9.7 8.6Zm2.29 1.65a2.75 2.75 0 1 1 0 5.5 2.75 2.75 0 0 1 0-5.5Z"
                fill="currentColor"
              />
            </svg>
          ) : (
            <svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true">
              <path
                d="M14.5 2.6a1 1 0 0 1 .8 1.63A7.5 7.5 0 1 0 19.77 8.7a1 1 0 0 1 1.63.8c0 6.07-4.93 11-11 11S-.6 15.57-.6 9.5 4.33-1.5 10.4-1.5a1 1 0 0 1 .8 1.63A7.49 7.49 0 0 0 14.5 2.6Z"
                transform="translate(1.6 3.5)"
                fill="currentColor"
              />
            </svg>
          )}
        </button>
        <button
          type="button"
          className={`save-state-btn save-state-${saveButtonState}`}
          onClick={() => void save()}
          disabled={saving || readOnly || !dirty}
          title={saveButtonState === "saving" ? "Saving..." : saveButtonState === "save" ? "Save" : "Saved"}
          aria-label={saveButtonState === "saving" ? "Saving..." : saveButtonState === "save" ? "Save" : "Saved"}
        >
          <span className="save-state-icon" aria-hidden="true">
            {saveButtonState === "saving" ? (
              <svg viewBox="0 0 24 24" width="20" height="20">
                <circle cx="12" cy="12" r="8.5" fill="none" stroke="currentColor" strokeWidth="2.5" opacity="0.28" />
                <path d="M12 3.5a8.5 8.5 0 0 1 8.5 8.5" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
              </svg>
            ) : saveButtonState === "save" ? (
              <svg viewBox="0 0 24 24" width="20" height="20">
                <path d="M5 3h10l4 4v14H5V3Zm2 2v5h10V7.8L14.2 5H7Zm2 9h6v5H9v-5Z" fill="currentColor" />
              </svg>
            ) : (
              <svg viewBox="0 0 24 24" width="20" height="20">
                <circle cx="12" cy="12" r="9.5" fill="currentColor" opacity="0.17" />
                <path
                  d="M8 12.2l2.6 2.8L16.4 9"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="2.4"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                />
              </svg>
            )}
          </span>
        </button>
        {asyncSaveState === "queued" || asyncSaveState === "running" ? (
          <span className="sync-badge async-save-badge">{asyncSaveState === "queued" ? "Syncing…" : "Syncing to git…"}</span>
        ) : null}
        {asyncSaveState === "failed" ? <span className="sync-badge async-save-badge is-error">Sync failed</span> : null}
      </header>
      <div className="wiki-body">
        <aside className="wiki-sidebar">
          <div className="sidebar-title">Pages</div>
          <Tree
            nodes={tree}
            activePath={path}
            onSelect={navigateToPath}
            onPageContextMenu={(e, pagePath) => {
              e.preventDefault();
              e.stopPropagation();
              setPageContextMenu({ x: e.clientX, y: e.clientY, pagePath });
            }}
            isCollapsed={(p) => !!collapsedPageNodes[p]}
            onToggleCollapse={(p) => {
              setCollapsedPageNodes((prev) => ({ ...prev, [p]: !prev[p] }));
            }}
          />
        </aside>

        <main className="wiki-main">
          <div className="editor-toolbar">
            <select
              className="tool-select"
              value={headingValue}
              onChange={(e) => applyHeading(e.target.value)}
              title="Headings"
              disabled={!canEdit}
            >
              <option value="paragraph">Paragraph</option>
              <option value="h1">Heading 1</option>
              <option value="h2">Heading 2</option>
              <option value="h3">Heading 3</option>
            </select>
            <select className="tool-select" value={formatValue} onChange={(e) => applyFormat(e.target.value)} title="Formats" disabled={!canEdit}>
              <option value="normal">Normal</option>
              <option value="bullet">Bullet list</option>
              <option value="ordered">Numbered list</option>
              <option value="quote">Quote</option>
              <option value="codeblock">Code block</option>
              <option value="inlinecode">Inline code</option>
            </select>

            <IconButton title="Bold" active={!!editor?.isActive("bold")} onClick={() => editor?.chain().focus().toggleBold().run()} disabled={!canEdit}>
              <FormatBoldIcon fontSize="small" />
            </IconButton>
            <IconButton title="Italic" active={!!editor?.isActive("italic")} onClick={() => editor?.chain().focus().toggleItalic().run()} disabled={!canEdit}>
              <FormatItalicIcon fontSize="small" />
            </IconButton>
            <IconButton
              title="Underline"
              active={!!editor?.isActive("underline")}
              onClick={() => editor?.chain().focus().toggleUnderline().run()}
              disabled={!canEdit}
            >
              <FormatUnderlinedIcon fontSize="small" />
            </IconButton>
            <IconButton title="Strike" active={!!editor?.isActive("strike")} onClick={() => editor?.chain().focus().toggleStrike().run()} disabled={!canEdit}>
              <StrikethroughSIcon fontSize="small" />
            </IconButton>
            <IconButton title="Link" active={!!editor?.isActive("link")} onClick={insertLink} disabled={!canEdit}>
              <InsertLinkIcon fontSize="small" />
            </IconButton>
            <button type="button" className="tool-btn tool-btn-text" onClick={triggerImageUpload} disabled={!canEdit}>
              Image
            </button>
            <button type="button" className="tool-btn tool-btn-text" onClick={() => void createDiagramAsset("excalidraw")} disabled={!canEdit}>
              Excalidraw
            </button>
            <button type="button" className="tool-btn tool-btn-text" onClick={() => void createDiagramAsset("drawio")} disabled={!canEdit}>
              draw.io
            </button>
          </div>

          <div className="editor-main">
            {!isEditing && refreshPrompt ? (
              <div className="draft-banner refresh-banner">
                <div>A newer saved version of this page is available.</div>
                <div className="draft-banner-actions">
                  <button type="button" onClick={() => setRefreshPrompt(null)}>
                    Later
                  </button>
                  <button type="button" onClick={() => void refreshViewerFromLatest()}>
                    Refresh
                  </button>
                </div>
              </div>
            ) : null}

            {draftBannerInfo ? (
              <div className="draft-banner">
                <div>
                  Unsaved draft found{draftBannerInfo.updated_at ? ` from ${new Date(draftBannerInfo.updated_at).toLocaleString()}` : ""}.
                  {draftBannerInfo.base_changed ? " This page changed since the draft was created." : ""}
                </div>
                <div className="draft-banner-actions">
                  <button type="button" onClick={() => void restoreDraft()}>
                    Restore
                  </button>
                  <button type="button" onClick={() => setCompareDraftOpen(true)}>
                    Compare
                  </button>
                  <button
                    type="button"
                    onClick={() => {
                      void discardDraft().catch((e) => setError(e instanceof Error ? e.message : "draft delete failed"));
                    }}
                  >
                    Discard
                  </button>
                </div>
              </div>
            ) : null}

            <div
              className="editor-container"
              onMouseMove={(e) => {
                const target = e.target as HTMLElement | null;
                if (target?.closest(".code-lang-pop")) {
                  return;
                }
                const pre = target?.closest("pre") as HTMLElement | null;
                if (pre) {
                  const info = findCodeBlockAtDOM(pre);
                  if (info) {
                    const rect = pre.getBoundingClientRect();
                    codeLangHoverRef.current = true;
                    setCodeLangHover({
                      x: rect.right - 184,
                      y: rect.top + 8,
                      pos: info.pos,
                      language: info.language || "plaintext",
                    });
                  }
                } else {
                  codeLangHoverRef.current = false;
                  setCodeLangHover(null);
                }
                const mark = target?.closest("mark[data-wiki-comment]") as HTMLElement | null;
                if (!mark) {
                  scheduleHidePopover();
                  return;
                }
                const anchorId = commentAnchorIdFromElement(mark);
                if (!anchorId || !threadsByAnchor[anchorId]) {
                  scheduleHidePopover();
                  return;
                }
                if (hideTimerRef.current) {
                  clearTimeout(hideTimerRef.current);
                }
                const rect = mark.getBoundingClientRect();
                setPopover({ anchorId, x: rect.left, y: rect.bottom + 8 });
              }}
              onMouseLeave={() => {
                codeLangHoverRef.current = false;
                setCodeLangHover(null);
                scheduleHidePopover();
              }}
              onContextMenu={(e) => {
                if (!editor || !canComment) {
                  return;
                }
                e.preventDefault();
                const sel = editor.state.selection;
                setContextMenu({
                  x: e.clientX,
                  y: e.clientY,
                  from: Math.min(sel.from, sel.to),
                  to: Math.max(sel.from, sel.to),
                });
              }}
              onClick={(e) => {
                const target = e.target as HTMLElement | null;
                const actionEl = target?.closest("[data-diagram-action]") as HTMLElement | null;
                if (actionEl) {
                  e.preventDefault();
                  e.stopPropagation();
                  const diagramPath = (actionEl.getAttribute("data-diagram-path") || "").trim();
                  const action = (actionEl.getAttribute("data-diagram-action") || "").trim();
                  if (diagramPath && action === "edit") {
                    void openDiagramEditor(diagramPath).catch((err) => setError(err instanceof Error ? err.message : "diagram load failed"));
                    return;
                  }
                  if (diagramPath && action === "open") {
                    window.open(assetApiURL(space, resolveAssetPath(path, diagramPath)), "_blank", "noopener,noreferrer");
                    return;
                  }
                }
                setContextMenu(null);
              }}
            >
              <input
                ref={imageInputRef}
                type="file"
                accept="image/*"
                className="hidden-file-input"
                onChange={(e) => {
                  const file = e.target.files?.[0];
                  e.currentTarget.value = "";
                  if (!file) {
                    return;
                  }
                  void uploadImageAndInsert(file).catch((err) => setError(err instanceof Error ? err.message : "image upload failed"));
                }}
              />
              <EditorContent editor={editor} />
              <DiagramPreview active={!isEditing} contentKey={markdown} theme={theme} />
              {codeLangHover ? (
                <div
                  className="code-lang-pop"
                  style={{ left: codeLangHover.x, top: codeLangHover.y }}
                  onMouseEnter={() => {
                    codeLangHoverRef.current = true;
                  }}
                  onMouseLeave={() => {
                    codeLangHoverRef.current = false;
                    setCodeLangHover(null);
                  }}
                >
                  <span className="code-lang-label">Language</span>
                  <select
                    value={codeLangHover.language || "plaintext"}
                    disabled={!canEdit}
                    onChange={(e) => {
                      if (!editor || !codeLangHover || !canEdit) {
                        return;
                      }
                      const lang = e.target.value;
                      editor
                        .chain()
                        .focus()
                        .setTextSelection(codeLangHover.pos + 1)
                        .updateAttributes("codeBlock", { language: lang })
                        .run();
                      setCodeLangHover((prev) => (prev ? { ...prev, language: lang } : prev));
                    }}
                  >
                    {CODE_LANGUAGES.map((lang) => (
                      <option key={lang} value={lang}>
                        {lang}
                      </option>
                    ))}
                  </select>
                </div>
              ) : null}
            </div>
          </div>

          {contextMenu ? (
            <div className="editor-context-menu" style={{ left: contextMenu.x, top: contextMenu.y }}>
              <button type="button" onClick={() => void addComment()}>
                Add comment
              </button>
            </div>
          ) : null}

          {pageContextMenu ? (
            <div
              className="editor-context-menu page-context-menu"
              style={{ left: pageContextMenu.x, top: pageContextMenu.y }}
              onContextMenu={(e) => e.preventDefault()}
            >
              <button
                type="button"
                title="Rename page"
                onClick={() => void renamePageAtPath(pageContextMenu.pagePath)}
              >
                Rename
              </button>
              <button
                type="button"
                className="danger-menu-item"
                title="Delete page"
                onClick={() => void deletePage(pageContextMenu.pagePath)}
              >
                Delete
              </button>
            </div>
          ) : null}
          {spaceContextMenu ? (
            <div
              className="editor-context-menu space-context-menu"
              style={{ left: spaceContextMenu.x, top: spaceContextMenu.y }}
              onContextMenu={(e) => e.preventDefault()}
            >
              <button
                type="button"
                onClick={() => {
                  setSpaceContextMenu(null);
                  void createSpace();
                }}
              >
                New space
              </button>
              <button
                type="button"
                title={isSpaceCreator ? "Rename space" : "Only the creator can rename this space"}
                onClick={() => {
                  setSpaceContextMenu(null);
                  void renameSpace();
                }}
                disabled={!isSpaceCreator}
              >
                Rename space
              </button>
              <button
                type="button"
                className="danger-menu-item"
                title={isSpaceCreator ? "Delete space" : "Only the creator can delete this space"}
                onClick={() => {
                  setSpaceContextMenu(null);
                  void deleteSpace();
                }}
                disabled={!isSpaceCreator}
              >
                Delete space
              </button>
            </div>
          ) : null}

          {popover && activeThread ? (
            <div
              className="comment-popover"
              style={{ left: popover.x, top: popover.y }}
              onMouseEnter={() => {
                popoverHoverRef.current = true;
                if (hideTimerRef.current) {
                  clearTimeout(hideTimerRef.current);
                }
              }}
              onMouseLeave={() => {
                popoverHoverRef.current = false;
                scheduleHidePopover();
              }}
            >
              <div className="comment-popover-title">
                Thread {activeThread.status === "resolved" ? "(resolved)" : ""}
              </div>
              <div className="comment-popover-list">
                {visibleThreadMessages(activeThread.messages).map((m) => (
                  <div key={m.hash_id} className="comment-item">
                    <div className="comment-meta">
                      <strong>{m.author_id}</strong>
                      <span>{new Date(m.updated_at || m.created_at).toLocaleString()}</span>
                    </div>
                    <div className="comment-body">{m.body}</div>
                    {m.can_edit ? (
                      <button
                        type="button"
                        className="comment-action"
                        onClick={() => {
                          const next = window.prompt("Edit comment", m.body);
                          if (!next || next.trim().length === 0) {
                            return;
                          }
                          void withThreadMutation(
                            () =>
                              fetch(`/api/spaces/${encodeURIComponent(space)}/comments/${encodeURIComponent(activeThread.thread_id)}/edit`, {
                                method: "POST",
                                credentials: "include",
                                headers: { "Content-Type": "application/json" },
                                body: JSON.stringify({
                                  path,
                                  hash_id: m.hash_id,
                                  comment: next.trim(),
                                  position: m.position,
                                }),
                              }),
                            `Comment edited ${new Date().toLocaleTimeString()}`,
                            (json) => {
                              const msg = json.message as CommentMessage | undefined;
                              if (!msg) {
                                return;
                              }
                              setThreadsByAnchor((prev) => {
                                const cur = prev[activeThread.anchor_id];
                                if (!cur) {
                                  return prev;
                                }
                                return {
                                  ...prev,
                                  [activeThread.anchor_id]: {
                                    ...cur,
                                    messages: [...cur.messages, msg],
                                  },
                                };
                              });
                            },
                          );
                        }}
                      >
                        Edit
                      </button>
                    ) : null}
                  </div>
                ))}
              </div>
              <div className="comment-popover-actions">
                <button
                  type="button"
                  className="comment-action"
                  onClick={() => {
                    const next = window.prompt("Reply");
                    if (!next || next.trim().length === 0) {
                      return;
                    }
                    void withThreadMutation(
                      () =>
                        fetch(`/api/spaces/${encodeURIComponent(space)}/comments/${encodeURIComponent(activeThread.thread_id)}/reply`, {
                          method: "POST",
                          credentials: "include",
                          headers: { "Content-Type": "application/json" },
                          body: JSON.stringify({
                            path,
                            comment: next.trim(),
                            position: activeThread.messages[0]?.position ?? 0,
                          }),
                        }),
                      `Reply added ${new Date().toLocaleTimeString()}`,
                      (json) => {
                        const msg = json.message as CommentMessage | undefined;
                        if (!msg) {
                          return;
                        }
                        setThreadsByAnchor((prev) => {
                          const cur = prev[activeThread.anchor_id];
                          if (!cur) {
                            return prev;
                          }
                          return {
                            ...prev,
                            [activeThread.anchor_id]: {
                              ...cur,
                              messages: [...cur.messages, msg],
                            },
                          };
                        });
                      },
                    );
                  }}
                >
                  Reply
                </button>
                {activeThread.status !== "resolved" ? (
                  <button
                    type="button"
                    className="comment-action danger"
                    onClick={() => {
                      void withThreadMutation(
                        () =>
                          fetch(`/api/spaces/${encodeURIComponent(space)}/comments/${encodeURIComponent(activeThread.thread_id)}/resolve`, {
                            method: "POST",
                            credentials: "include",
                            headers: { "Content-Type": "application/json" },
                            body: JSON.stringify({ path }),
                          }),
                        `Thread resolved ${new Date().toLocaleTimeString()}`,
                        () => {
                          if (editor) {
                            removeCommentHighlightMarks(editor, activeThread.anchor_id);
                          }
                          setThreadsByAnchor((prev) => {
                            if (!prev[activeThread.anchor_id]) return prev;
                            const next = { ...prev };
                            delete next[activeThread.anchor_id];
                            return next;
                          });
                          setPopover(null);
                          setContextMenu(null);
                          setDirty(true);
                        },
                      );
                    }}
                  >
                    Resolve
                  </button>
                ) : null}
              </div>
            </div>
          ) : null}

          {settingsOpen ? (
            <div className="settings-backdrop" onClick={() => setSettingsOpen(false)}>
              <div className="settings-modal" onClick={(e) => e.stopPropagation()}>
                <div className="settings-header">
                  <strong>Settings</strong>
                  <button type="button" className="top-icon-btn" onClick={() => setSettingsOpen(false)} title="Close">
                    x
                  </button>
                </div>
                <div className="settings-grid">
                  <div className="settings-label">Root Git Repo</div>
                  <div>{settingsInfo?.settings?.root_repo_url || "not set"}</div>

                  <div className="settings-label">Root Local Directory</div>
                  <div>{settingsInfo?.settings?.root_repo_local_dir || "not set"}</div>

                  <div className="settings-label">Space Settings File</div>
                  <div>{settingsInfo?.storage?.root_settings || "not set"}</div>

                  <div className="settings-label">Storage Implementation</div>
                  <div>{settingsInfo?.storage?.implementation || "local_file"}</div>

                  <div className="settings-label">Local Storage Path</div>
                  <div>{settingsInfo?.storage?.local_settings || "not set"}</div>

                  <div className="settings-label">Storage Directory</div>
                  <div>{settingsInfo?.storage?.storage_dir || settingsInfo?.settings?.storage_dir || "not set"}</div>

                  <div className="settings-label">Save Mode</div>
                  <div className="settings-inline">
                    <select
                      value={settingsSaveMode}
                      onChange={(e) => setSettingsSaveMode(e.target.value as "local" | "git_sync")}
                    >
                      <option value="local">Local save (filesystem only)</option>
                      <option value="git_sync">Git sync (commit + push)</option>
                    </select>
                    <button type="button" onClick={() => void saveSettings()} disabled={settingsSaving}>
                      {settingsSaving ? "Saving…" : "Apply"}
                    </button>
                  </div>
                </div>
              </div>
            </div>
          ) : null}

          {createPageOpen ? (
            <div className="settings-backdrop" onClick={() => setCreatePageOpen(false)}>
              <div className="settings-modal create-page-modal" onClick={(e) => e.stopPropagation()}>
                <div className="settings-header">
                  <strong>Create page</strong>
                  <button type="button" className="top-icon-btn" onClick={() => setCreatePageOpen(false)} title="Close">
                    x
                  </button>
                </div>
                <div className="create-page-form">
                  <label>
                    Path
                    <input
                      value={createPageInput}
                      onChange={(e) => setCreatePageInput(e.target.value)}
                      placeholder="new-page.md"
                      autoFocus
                    />
                  </label>
                  <label>
                    Parent
                    <select value={createParent} onChange={(e) => setCreateParent(e.target.value)}>
                      {createParentOptions.map((opt) => (
                        <option key={opt.value} value={opt.value}>
                          {opt.label}
                        </option>
                      ))}
                    </select>
                  </label>
                  <div className="create-page-preview">Creates: {createPageSuggestion}</div>
                  <div className="settings-inline">
                    <button type="button" onClick={() => setCreatePageOpen(false)}>
                      Cancel
                    </button>
                    <button type="button" onClick={() => void createPage()} disabled={!createPageInput.trim()}>
                      Create
                    </button>
                  </div>
                </div>
              </div>
            </div>
          ) : null}

          {pendingNavigation ? (
            <div className="settings-backdrop" onClick={() => void cancelPendingNavigation()}>
              <div className="settings-modal unsaved-navigation-modal" onClick={(e) => e.stopPropagation()}>
                <div className="settings-header">
                  <strong>Unsaved changes</strong>
                  <button
                    type="button"
                    className="top-icon-btn"
                    onClick={() => void cancelPendingNavigation()}
                    title="Close"
                    disabled={navigationDecisionBusy !== null}
                  >
                    x
                  </button>
                </div>
                <div className="unsaved-navigation-copy">
                  You have unsaved changes on this page. Save them before switching {pendingNavigation.kind === "space" ? "spaces" : "pages"}?
                </div>
                <div className="settings-inline">
                  <button type="button" onClick={() => void cancelPendingNavigation()} disabled={navigationDecisionBusy !== null}>
                    Cancel
                  </button>
                  <button type="button" onClick={() => void discardAndContinueNavigation()} disabled={navigationDecisionBusy !== null}>
                    {navigationDecisionBusy === "discard" ? "Discarding…" : "Discard"}
                  </button>
                  <button type="button" onClick={() => void saveAndContinueNavigation()} disabled={navigationDecisionBusy !== null || saving}>
                    {navigationDecisionBusy === "save" || saving ? "Saving…" : "Save"}
                  </button>
                </div>
              </div>
            </div>
          ) : null}

          {compareDraftOpen && draftInfo?.markdown ? (
            <div className="settings-backdrop" onClick={() => setCompareDraftOpen(false)}>
              <div className="settings-modal draft-compare-modal" onClick={(e) => e.stopPropagation()}>
                <div className="settings-header">
                  <strong>Compare saved page and recovered draft</strong>
                  <button type="button" className="top-icon-btn" onClick={() => setCompareDraftOpen(false)} title="Close">
                    x
                  </button>
                </div>
                <div className="draft-compare-grid">
                  <div>
                    <div className="draft-compare-label">Saved page</div>
                    <pre className="error-details-pre">{lastSavedMarkdownRef.current || markdown}</pre>
                  </div>
                  <div>
                    <div className="draft-compare-label">Recovered draft</div>
                    <pre className="error-details-pre">{draftInfo.markdown}</pre>
                  </div>
                </div>
                <div className="settings-inline">
                  <button type="button" onClick={() => setCompareDraftOpen(false)}>
                    Close
                  </button>
                  <button
                    type="button"
                    onClick={() => {
                      void restoreDraft();
                      setCompareDraftOpen(false);
                    }}
                  >
                    Restore draft
                  </button>
                </div>
              </div>
            </div>
          ) : null}

          {diagramEditor ? (
            <div className="settings-backdrop" onClick={() => setDiagramEditor(null)}>
              <div className="settings-modal diagram-editor-modal" onClick={(e) => e.stopPropagation()}>
                <div className="settings-header">
                  <strong>Edit {diagramEditor.kind}</strong>
                  <button type="button" className="top-icon-btn" onClick={() => setDiagramEditor(null)} title="Close">
                    x
                  </button>
                </div>
                <div className="diagram-editor-path">{diagramEditor.path}</div>
                <textarea
                  className="diagram-editor-textarea"
                  value={diagramEditor.content}
                  onChange={(e) =>
                    setDiagramEditor((prev) => (prev ? { ...prev, content: e.target.value } : prev))
                  }
                />
                <div className="settings-inline">
                  <button type="button" onClick={() => setDiagramEditor(null)}>
                    Cancel
                  </button>
                  <button type="button" onClick={() => void saveDiagramEditor()}>
                    Save diagram
                  </button>
                </div>
              </div>
            </div>
          ) : null}

          {errorDetailsOpen ? (
            <div className="settings-backdrop" onClick={() => setErrorDetailsOpen(false)}>
              <div className="settings-modal error-details-modal" onClick={(e) => e.stopPropagation()}>
                <div className="settings-header">
                  <strong>Error details</strong>
                  <button type="button" className="top-icon-btn" onClick={() => setErrorDetailsOpen(false)} title="Close">
                    x
                  </button>
                </div>
                <pre className="error-details-pre">{lastErrorDetails || error || "No details available."}</pre>
              </div>
            </div>
          ) : null}

          <div className="editor-status">
            {status || (dirty ? "Unsaved changes (draft autosave active)" : "All changes saved")}
            {error ? (
              <>
                {" "}
                ·{" "}
                <button type="button" className="status-error-btn" onClick={() => setErrorDetailsOpen(true)}>
                  {error}
                </button>
              </>
            ) : null}
          </div>
        </main>
      </div>
    </div>
  );
}
