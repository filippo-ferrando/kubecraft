export function isDark() {
  return document.documentElement.classList.contains("dark");
}

export function toggleTheme(): boolean {
  const dark = !isDark();
  document.documentElement.classList.toggle("dark", dark);
  localStorage.setItem("theme", dark ? "dark" : "light");
  return dark;
}
