# Git layout (mount-style spaces)

Each **space** maps to a **git repository** root (clone target) via [spaces-registry.yaml](../spaces-registry.yaml).

## Tree (per space)

```text
<space_root>/
  .mdwiki/
    space.json
    index.json              # optional routing index (page_id, path, title)
    comments/
      <page_key>/
        <thread_id>.json    # one file per discussion thread
  pages/                     # optional; or markdown at repo root
  assets/
```

## `page_key`

Stable identifier for a page used in `.mdwiki/comments/<page_key>/`. Options:

- Hash of relative path to `*.md` (recommended for rename resilience with index update), or
- URL-safe slug derived from path.

## Rename / move

When a markdown file moves, either:

- Move the corresponding `comments/<page_key>/` directory and update `index.json`, or
- Re-key comments (new `page_key`) and migrate thread files.

## Sidecars

- `page.mdwiki.json` — metadata only (title, slug, parent), **no** comment bodies.
- Thread JSON lives only under `.mdwiki/comments/`.
