// The API call of the Resources page (internal/api/toolbox.go). Types and everything pure are
// in lib/toolbox-format.ts.
import { useStore } from "./store";
import type { ResourceList } from "./toolbox-format";

export const toolboxApi = {
  list: () => {
    const tr = useStore.getState().transport;
    if (!tr) throw new Error("Not connected yet.");
    return tr.request<ResourceList>("GET", "/api/toolbox");
  },
};
