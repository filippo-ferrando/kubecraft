// Applied before first paint to avoid a flash of the wrong theme.
if (localStorage.getItem("theme") === "light") document.documentElement.classList.remove("dark");
