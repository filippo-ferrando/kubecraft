import { describe, expect, it } from "vitest";
import { cpu, mem, planBlocked } from "./format";

const plan = { id: "p", name: "Small", cpu_request_m: 500, cpu_limit_m: 1000, memory_limit_mb: 2048, storage_gb: 5, active: true };
const none = { max_servers: null, max_total_cpu_m: null, max_total_memory_mb: null, max_total_storage_gb: null };
const empty = { servers: 0, storage_gb: 0, running_cpu_m: 0, running_memory_mb: 0 };

describe("format", () => {
  it("formats resources", () => {
    expect(cpu(1000)).toBe("1 CPU");
    expect(cpu(1500)).toBe("1.5 CPU");
    expect(cpu(22)).toBe("22m CPU");
    expect(mem(2048)).toBe("2 GB");
    expect(mem(512)).toBe("512 MB");
  });
  it("checks plans against the quota", () => {
    expect(planBlocked(plan, none, empty)).toBe("");
    expect(planBlocked(plan, { ...none, max_servers: 2 }, { ...empty, servers: 2 })).toContain("server limit");
    expect(planBlocked(plan, { ...none, max_total_memory_mb: 3072 }, { ...empty, running_memory_mb: 2048 })).toContain("RAM");
  });
});
