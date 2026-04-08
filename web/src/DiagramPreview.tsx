import { useEffect, useRef } from "react";

type DiagramPreviewProps = {
  active: boolean;
  contentKey: string;
  theme: "light" | "dark";
};

type ChartHandle = {
  destroy: () => void;
};

type JsonRecord = Record<string, unknown>;

function parseJSON(source: string): JsonRecord {
  const parsed = JSON.parse(source) as unknown;
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("diagram block must contain a JSON object");
  }
  return parsed as JsonRecord;
}

function createHost(kind: string, source: string): HTMLDivElement {
  const host = document.createElement("div");
  host.className = "diagram-preview";
  host.dataset.diagramKind = kind;
  host.dataset.diagramSource = source;
  return host;
}

function createError(kind: string, message: string): HTMLDivElement {
  const host = document.createElement("div");
  host.className = "diagram-preview diagram-preview-error";
  host.dataset.diagramKind = kind;

  const title = document.createElement("strong");
  title.textContent = `${kind} render failed`;

  const body = document.createElement("div");
  body.className = "diagram-preview-error-text";
  body.textContent = message;

  host.append(title, body);
  return host;
}

function replacePre(code: Element, nextNode: HTMLElement) {
  const pre = code.parentElement;
  if (!pre?.parentNode) {
    return;
  }
  pre.parentNode.replaceChild(nextNode, pre);
}

async function renderMermaid(source: string, theme: "light" | "dark"): Promise<HTMLElement> {
  const mermaid = (await import("mermaid")).default;
  mermaid.initialize({ startOnLoad: false, theme: theme === "dark" ? "dark" : "default" });
  const id = `mdwiki-mermaid-${Math.random().toString(36).slice(2)}`;
  const { svg } = await mermaid.render(id, source);
  const host = createHost("mermaid", source);
  host.innerHTML = svg;
  return host;
}

async function renderGraphviz(source: string): Promise<HTMLElement> {
  const { instance } = await import("@viz-js/viz");
  const viz = await instance();
  const svg = viz.renderString(source, { format: "svg", engine: "dot" });
  const host = createHost("graphviz", source);
  host.innerHTML = svg;
  return host;
}

async function renderChart(source: string): Promise<{ element: HTMLElement; handle: ChartHandle }> {
  const spec = parseJSON(source);
  const Chart = (await import("chart.js/auto")).default;
  const canvas = document.createElement("canvas");
  const host = createHost("chart", source);
  host.appendChild(canvas);
  const chart = new Chart(canvas, spec as never);
  return { element: host, handle: { destroy: () => chart.destroy() } };
}

async function renderVega(source: string, mode: "vega" | "vega-lite"): Promise<HTMLElement> {
  const spec = parseJSON(source);
  const embed = (await import("vega-embed")).default;
  const host = createHost(mode, source);
  await embed(host, spec, { actions: false, mode, renderer: "svg" });
  return host;
}

async function renderPlantUML(source: string): Promise<HTMLElement> {
  const encoderModule = (await import("plantuml-encoder")) as { default?: { encode: (text: string) => string }; encode?: (text: string) => string };
  const encode = encoderModule.default?.encode ?? encoderModule.encode;
  if (!encode) {
    throw new Error("PlantUML encoder unavailable");
  }
  const img = document.createElement("img");
  img.className = "diagram-preview-image";
  img.alt = "PlantUML diagram";
  img.loading = "lazy";
  img.src = `https://www.plantuml.com/plantuml/svg/${encode(source)}`;
  const host = createHost("plantuml", source);
  host.appendChild(img);
  return host;
}

/** Upgrade supported fenced code blocks into rendered diagrams in viewing mode. */
export function DiagramPreview({ active, contentKey, theme }: DiagramPreviewProps) {
  const chartHandles = useRef<ChartHandle[]>([]);

  useEffect(() => {
    chartHandles.current.forEach((handle) => handle.destroy());
    chartHandles.current = [];

    if (!active || !contentKey) {
      return;
    }

    let cancelled = false;
    const root = document.querySelector(".md-preview");
    if (!root) {
      return;
    }

    const renderers: Record<string, (source: string) => Promise<HTMLElement | { element: HTMLElement; handle?: ChartHandle }>> = {
      mermaid: (source) => renderMermaid(source, theme),
      chart: renderChart,
      graphviz: renderGraphviz,
      dot: renderGraphviz,
      vega: (source) => renderVega(source, "vega"),
      "vega-lite": (source) => renderVega(source, "vega-lite"),
      plantuml: renderPlantUML,
    };

    const run = async () => {
      const codes = Array.from(root.querySelectorAll("pre code")).filter((code) => {
        const className = code.getAttribute("class") || "";
        return Object.keys(renderers).some((kind) => className.split(/\s+/).includes(`language-${kind}`));
      });

      for (const code of codes) {
        if (cancelled) {
          break;
        }
        const className = code.getAttribute("class") || "";
        const kind = Object.keys(renderers).find((candidate) => className.split(/\s+/).includes(`language-${candidate}`));
        if (!kind) {
          continue;
        }
        const source = code.textContent || "";
        try {
          const rendered = await renderers[kind](source);
          if (cancelled) {
            if ("handle" in rendered && rendered.handle) {
              rendered.handle.destroy();
            }
            break;
          }
          const element = "element" in rendered ? rendered.element : rendered;
          if ("handle" in rendered && rendered.handle) {
            chartHandles.current.push(rendered.handle);
          }
          replacePre(code, element);
        } catch (error) {
          replacePre(code, createError(kind, error instanceof Error ? error.message : "unknown renderer error"));
        }
      }
    };

    void run();

    return () => {
      cancelled = true;
      chartHandles.current.forEach((handle) => handle.destroy());
      chartHandles.current = [];
    };
  }, [active, contentKey, theme]);

  return null;
}
