import { liftListItem, sinkListItem } from "@tiptap/pm/schema-list";
import type { EditorView } from "@tiptap/pm/view";

export function handleEditorTab(view: EditorView, event: KeyboardEvent): boolean {
  if (event.key !== "Tab") {
    return false;
  }

  const { state } = view;
  const listItemType = state.schema.nodes.listItem;
  const taskItemType = state.schema.nodes.taskItem;
  const command = event.shiftKey ? liftListItem : sinkListItem;

  for (const nodeType of [taskItemType, listItemType]) {
    if (!nodeType) {
      continue;
    }
    const applied = command(nodeType)(state, view.dispatch);
    if (applied) {
      event.preventDefault();
      return true;
    }
  }

  event.preventDefault();
  const { from, to } = state.selection;
  view.dispatch(state.tr.insertText("\t", from, to).scrollIntoView());
  return true;
}
