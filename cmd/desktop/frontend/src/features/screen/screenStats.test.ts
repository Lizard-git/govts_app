import {describe, expect, it} from "vitest";
import {ScreenStatsCollector} from "./screenStats";

function report(...values: any[]): RTCStatsReport {
    return {forEach: (callback: (value: any) => void) => values.forEach(callback)} as RTCStatsReport;
}

describe("ScreenStatsCollector", () => {
    it("calculates delta bitrate, fps and loss", () => {
        const collector = new ScreenStatsCollector();
        collector.collect(report({id: "v", type: "inbound-rtp", kind: "video", ssrc: 1, timestamp: 1000, bytesReceived: 1000, packetsReceived: 10, packetsLost: 1, framesDecoded: 5}), "inbound-rtp");
        const value = collector.collect(report({id: "v", type: "inbound-rtp", kind: "video", ssrc: 1, timestamp: 2000, bytesReceived: 251000, packetsReceived: 109, packetsLost: 2, framesDecoded: 35, frameWidth: 1920, frameHeight: 1080, jitter: .008}), "inbound-rtp");
        expect(value?.bitrate).toBe(2_000_000);
        expect(value?.fps).toBe(30);
        expect(value?.lossPercent).toBe(1);
        expect(value?.jitterMs).toBe(8);
    });

    it("resets baseline when SSRC changes", () => {
        const collector = new ScreenStatsCollector();
        collector.collect(report({id: "a", type: "inbound-rtp", kind: "video", ssrc: 1, timestamp: 1000, bytesReceived: 100}), "inbound-rtp");
        expect(collector.collect(report({id: "b", type: "inbound-rtp", kind: "video", ssrc: 2, timestamp: 2000, bytesReceived: 1000}), "inbound-rtp")?.bitrate).toBe(0);
    });

    it("uses safe defaults when optional browser stats are absent", () => {
        const value = new ScreenStatsCollector().collect(report({
            id: "v", type: "outbound-rtp", kind: "video", timestamp: 1000,
        }), "outbound-rtp");
        expect(value).toMatchObject({
            bitrate: 0, fps: 0, width: 0, height: 0, packetsLost: 0,
            lossSampleAvailable: false,
            frames: 0, framesDropped: 0, freezeCount: 0, freezeDurationMs: 0,
            keyFrames: 0, qualityLimitation: "",
        });
    });
});
