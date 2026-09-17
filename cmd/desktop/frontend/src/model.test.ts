import { describe, expect, it } from "vitest";
import type {ChannelDTO, ClientEventDTO} from "./api";
import { buildChannelGroups, mergeEventTail } from "./model";

const audio = { codec: "opus", sampleRate: 48000, channels: 1, frameDurationMs: 20, bitrate: 24000, application: "voip" };
const channel = (id: string, parentId: string, position: number): ChannelDTO => ({
  id, parentId, position, name: id, topic: "", description: "", maxUsers: 0, audio,
});

describe("buildChannelGroups", () => {
  it("builds hierarchy independently of input order and sorts numeric uint64 IDs", () => {
    const groups = buildChannelGroups([
      channel("18446744073709551615", "1", 0),
      channel("10", "0", 1),
      channel("1", "0", 0),
      channel("9007199254740992", "1", 0),
    ]);
    expect(groups.get("0")?.map((item) => item.id)).toEqual(["1", "10"]);
    expect(groups.get("1")?.map((item) => item.id)).toEqual(["9007199254740992", "18446744073709551615"]);
  });

  it("places a channel with an unknown parent at the root", () => {
    expect(buildChannelGroups([channel("2", "999", 0)]).get("0")?.[0].id).toBe("2");
  });
});

describe("mergeEventTail", () => {
  it("deduplicates concurrent deliveries and keeps sequence order", () => {
    const event = (sequence: string): ClientEventDTO => ({ sequence, time: "2026-09-14T00:00:00Z", kind: "server", message: sequence });
    expect(mergeEventTail([event("10"), event("2")], [event("10"), event("3")]).map((item) => item.sequence)).toEqual(["2", "3", "10"]);
  });

  it("keeps only the newest 200 records", () => {
    const events = Array.from({ length: 205 }, (_, index) => ({ sequence: String(index + 1), time: "", kind: "server", message: "" }));
    const result = mergeEventTail([], events);
    expect(result).toHaveLength(200);
    expect(result[0].sequence).toBe("6");
  });
});
