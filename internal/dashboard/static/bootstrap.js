"use strict";
// Only integration glue. All visual components and polling belong to AMIS.
(async () => {
  const response = await fetch("config.json", {cache: "no-store"});
  if (!response.ok) throw new Error("Dashboard configuration unavailable");
  const {schema, theme, refreshMs} = await response.json();
  document.getElementById("theme").href = "vendor/" + theme + ".css";
  amisRequire("amis/embed").embed("#root", schema, {
    data: {refreshMs},
    locale: "en-US"
  }, {
    theme,
    locale: "en-US",
    fetcher: async ({url, method = "get"}) => {
      const target = new URL(url, location.origin);
      if (method.toLowerCase() !== "get" || target.origin !== location.origin ||
          !target.pathname.startsWith("/api/v1/")) {
        throw new Error("Dashboard only permits same-origin API reads");
      }
      const res = await fetch(target, {cache: "no-store", redirect: "error", signal: AbortSignal.timeout(15000)});
      if (!res.ok) throw new Error("API request failed: " + res.status);
      const data = await res.json();
      return {status: res.status, data: {status: 0, data: Array.isArray(data) ? {items: data} : data}};
    }
  });
})().catch(error => {
  document.getElementById("root").textContent = "Dashboard unavailable: " + error.message;
});
