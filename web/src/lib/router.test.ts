import { describe, expect, it, vi } from "vitest";

vi.stubGlobal("location", { pathname: "/", search: "", origin: "http://x" });
vi.stubGlobal("window", { addEventListener: () => {}, scrollTo: () => {} });

const { match } = await import("./router.svelte");

describe("match", () => {
  it("matches static and param segments", () => {
    expect(match("/servers/:id", "/servers/abc")).toEqual({ id: "abc" });
    expect(match("/servers/:id/:tab", "/servers/abc/console")).toEqual({ id: "abc", tab: "console" });
    expect(match("/", "/")).toEqual({});
  });
  it("rejects mismatches", () => {
    expect(match("/servers/:id", "/servers")).toBeNull();
    expect(match("/admin/users", "/admin/plans")).toBeNull();
  });
});
