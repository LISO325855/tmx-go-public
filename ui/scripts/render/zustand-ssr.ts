// zustand's hook reads getInitialState() while rendering on a server; this render wants the
// state the mock daemon filled in, so the initial state IS the current state.
import { create as real } from "zustand/react";
export * from "zustand/vanilla";
export { useStore } from "zustand/react";
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export const create: any = (init?: any) => {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const wrap = (i: any) => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const hook: any = (real as any)(i);
    hook.getInitialState = hook.getState;
    return hook;
  };
  return init ? wrap(init) : wrap;
};
