import { Schema } from "@tiptap/pm/model";
import { EditorState, TextSelection } from "@tiptap/pm/state";
import { EditorView } from "@tiptap/pm/view";
import { beforeEach, describe, expect, it } from "vitest";
import { handleEditorTab } from "./editorTab";

const schema = new Schema({
  nodes: {
    doc: {
      content: "paragraph+",
    },
    text: {
      group: "inline",
    },
    paragraph: {
      content: "text*",
      group: "block",
      toDOM() {
        return ["p", 0];
      },
    },
  },
});

describe("handleEditorTab", () => {
  let host: HTMLDivElement;

  beforeEach(() => {
    host = document.createElement("div");
    document.body.innerHTML = "";
    document.body.appendChild(host);
  });

  it("inserts a literal tab in a paragraph selection", () => {
    const doc = schema.node("doc", undefined, [schema.node("paragraph", undefined, [schema.text("alpha")])]);
    const state = EditorState.create({
      schema,
      doc,
      selection: TextSelection.create(doc, 1),
    });

    const view = new EditorView(host, { state });
    const event = new KeyboardEvent("keydown", { key: "Tab", bubbles: true, cancelable: true });

    const handled = handleEditorTab(view, event);

    expect(handled).toBe(true);
    expect(event.defaultPrevented).toBe(true);
    expect(view.state.doc.textBetween(0, view.state.doc.content.size, "\n")).toBe("\talpha");
    view.destroy();
  });
});
