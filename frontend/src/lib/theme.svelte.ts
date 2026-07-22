function resolveInitialDark(): boolean {
  const stored = localStorage.getItem("theme");
  if (stored === "dark") return true;
  if (stored === "light") return false;
  return window.matchMedia("(prefers-color-scheme: dark)").matches;
}

let _dark = $state(resolveInitialDark());

export const theme = {
  get dark() {
    return _dark;
  },
  toggle() {
    _dark = !_dark;
    const t = _dark ? "dark" : "light";
    localStorage.setItem("theme", t);
    document.documentElement.dataset.theme = t;
  },
};
