// The colours an installed app shows before and around the page: the browser's bar (theme-color),
// the splash screen (the install file's background_color) and the first paint. They are the page
// background of the Tiamatrix green in each mode (index.css --background), so opening the app
// never flashes a different colour. Pure, so node can check index.html, the install file and the
// stylesheet against it (test/pwa-manifest.test.mjs).
export const PAGE_BACKGROUND = { light: "#f1f5ed", dark: "#151e19" } as const;

/** The install file cannot follow the mode: it opens light, in Tiamatrix green, as Python's does. */
export const INSTALL_BACKGROUND = PAGE_BACKGROUND.light;

export function pageBackground(dark: boolean): string {
  return dark ? PAGE_BACKGROUND.dark : PAGE_BACKGROUND.light;
}
