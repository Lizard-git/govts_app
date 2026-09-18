import {desktopAPI} from "./api";
import {ScreenStatsCollector, type ScreenStats} from "./screenStats";

export type ScreenProfileID = "economy" | "text" | "standard";
export type ScreenProfile = {
    id: ScreenProfileID;
    label: string;
    width: number;
    height: number;
    minFrameRate: number;
    frameRate: number;
    bitrate: number;
    contentHint: "detail" | "motion";
    degradationPreference: "maintain-framerate" | "maintain-resolution";
};
export const screenProfiles: ScreenProfile[] = [
    {id: "economy", label: "Экономный · 720p/15", width: 1280, height: 720, minFrameRate: 10, frameRate: 15, bitrate: 1_000_000, contentHint: "motion", degradationPreference: "maintain-framerate"},
    {id: "text", label: "Текст · 1080p/15", width: 1920, height: 1080, minFrameRate: 10, frameRate: 15, bitrate: 2_000_000, contentHint: "detail", degradationPreference: "maintain-resolution"},
    {id: "standard", label: "Стандартный · 1080p/30", width: 1920, height: 1080, minFrameRate: 15, frameRate: 30, bitrate: 2_500_000, contentHint: "motion", degradationPreference: "maintain-framerate"},
];
export const defaultScreenProfile = screenProfiles[2];

function waitForGathering(pc: RTCPeerConnection): Promise<void> {
    if (pc.iceGatheringState === "complete") return Promise.resolve();
    return new Promise((resolve, reject) => {
        const timeout = window.setTimeout(() => finish(new Error("Истекло время подготовки WebRTC offer")), 10_000);
        const changed = () => {
            if (pc.iceGatheringState === "complete") finish();
            else if (pc.connectionState === "closed") finish(new DOMException("Операция отменена", "AbortError"));
        };
        const finish = (error?: Error) => {
            window.clearTimeout(timeout);
            pc.removeEventListener("icegatheringstatechange", changed);
            pc.removeEventListener("connectionstatechange", changed);
            if (error) reject(error); else resolve();
        };
        pc.addEventListener("icegatheringstatechange", changed);
        pc.addEventListener("connectionstatechange", changed);
    });
}

async function localOffer(pc: RTCPeerConnection): Promise<{type: string; sdp: string}> {
    await pc.setLocalDescription(await pc.createOffer());
    await waitForGathering(pc);
    if (!pc.localDescription?.sdp) throw new Error("Не удалось создать WebRTC offer");
    return {type: "offer", sdp: pc.localDescription.sdp};
}

function waitForConnected(pc: RTCPeerConnection, timeoutMs = 20_000): Promise<void> {
    if (pc.connectionState === "connected") return Promise.resolve();
    return new Promise((resolve, reject) => {
        const timeout = window.setTimeout(() => finish(new Error("Не удалось установить WebRTC media-соединение. Проверьте UDP-порты сервера и advertised IP")), timeoutMs);
        const changed = () => {
            if (pc.connectionState === "connected") finish();
            if (pc.connectionState === "failed" || pc.connectionState === "closed") {
                finish(new Error("WebRTC media-соединение не установлено. Проверьте UDP-порты сервера и advertised IP"));
            }
        };
        const finish = (error?: Error) => {
            window.clearTimeout(timeout);
            pc.removeEventListener("connectionstatechange", changed);
            if (error) reject(error); else resolve();
        };
        pc.addEventListener("connectionstatechange", changed);
    });
}

async function ensureTrusted(): Promise<void> {
    const identity = await desktopAPI.mediaServerIdentity();
    if (identity.trusted) return;
    if (identity.known) throw new Error(`Ключ media-сервера изменился. Новый отпечаток: ${identity.fingerprint}`);
    await desktopAPI.trustMediaServer(identity.fingerprint);
}

export class ScreenMediaController {
    private publisher?: {streamID: string; pc: RTCPeerConnection; capture: MediaStream};
    private pendingPublisher?: {pc: RTCPeerConnection; capture: MediaStream};
    private viewer?: {streamID: string; subscriberID: string; pc: RTCPeerConnection};
    private publisherStats = new ScreenStatsCollector();
    private viewerStats = new ScreenStatsCollector();

    async publish(onEnded: () => void, profile: ScreenProfile = defaultScreenProfile): Promise<string> {
        if (this.publisher) return this.publisher.streamID;
        // Keep the picker in the direct click call chain: some WebViews require user activation.
        const capture = await navigator.mediaDevices.getDisplayMedia({video: true, audio: false});
        const pc = new RTCPeerConnection({iceServers: []});
        const pending = {pc, capture};
        this.pendingPublisher = pending;
        let streamID = "";
        try {
            const captureTrack = capture.getVideoTracks()[0];
            if (!captureTrack) throw new Error("Источник не предоставил видеотрек");
            captureTrack.onended = () => pc.close();
            captureTrack.contentHint = profile.contentHint;
            try {
                await captureTrack.applyConstraints({
                    width: {ideal: profile.width, max: profile.width},
                    height: {ideal: profile.height, max: profile.height},
                    frameRate: {min: profile.minFrameRate, ideal: profile.frameRate, max: profile.frameRate},
                });
            } catch {
                // Some capture backends reject minimum frame-rate constraints.
                // Retain the preferred target without aborting screen sharing.
                await captureTrack.applyConstraints({
                    width: {ideal: profile.width, max: profile.width},
                    height: {ideal: profile.height, max: profile.height},
                    frameRate: {ideal: profile.frameRate, max: profile.frameRate},
                }).catch(() => undefined);
            }
            const transceiver = pc.addTransceiver(captureTrack, {
                direction: "sendonly",
                streams: [capture],
                sendEncodings: [{maxBitrate: profile.bitrate}],
            });
            const senderParameters = transceiver.sender.getParameters();
            senderParameters.degradationPreference = profile.degradationPreference;
            await transceiver.sender.setParameters(senderParameters).catch(() => undefined);
            await ensureTrusted();
            const result = await desktopAPI.publishScreen(await localOffer(pc));
            await pc.setRemoteDescription({type: "answer", sdp: result.answer.sdp});
            streamID = result.streamId;
            await waitForConnected(pc);
            if (this.pendingPublisher !== pending) throw new DOMException("Операция отменена", "AbortError");
            this.pendingPublisher = undefined;
            this.publisher = {streamID, pc, capture};
            captureTrack.onended = () => void this.stopPublishing().finally(onEnded);
            return result.streamId;
        } catch (error) {
            if (this.pendingPublisher === pending) this.pendingPublisher = undefined;
            capture.getTracks().forEach((track) => track.stop());
            pc.close();
            if (streamID) await desktopAPI.stopScreen(streamID).catch(() => undefined);
            throw error;
        }
    }

    async stopPublishing(): Promise<void> {
        const active = this.publisher;
        const pending = this.pendingPublisher;
        this.publisher = undefined;
        this.pendingPublisher = undefined;
        this.publisherStats.reset();
        if (pending) {
            pending.capture.getTracks().forEach((track) => track.stop());
            pending.pc.close();
        }
        if (!active) return;
        active.capture.getTracks().forEach((track) => track.stop());
        active.pc.close();
        await desktopAPI.stopScreen(active.streamID);
    }

    async subscribe(streamID: string): Promise<MediaStream> {
        await this.unsubscribe();
        await ensureTrusted();
        const pc = new RTCPeerConnection({iceServers: []});
        const remote = new MediaStream();
        pc.ontrack = (event) => remote.addTrack(event.track);
        pc.addTransceiver("video", {direction: "recvonly"});
        const subscriberID = crypto.randomUUID();
        try {
            const result = await desktopAPI.subscribeScreen(streamID, subscriberID, await localOffer(pc));
            await pc.setRemoteDescription({type: "answer", sdp: result.answer.sdp});
            this.viewer = {streamID, subscriberID, pc};
            this.viewerStats.reset();
            return remote;
        } catch (error) {
            pc.close();
            throw error;
        }
    }

    async unsubscribe(): Promise<void> {
        const active = this.viewer;
        this.viewer = undefined;
        this.viewerStats.reset();
        if (!active) return;
        active.pc.close();
        await desktopAPI.unsubscribeScreen(active.streamID, active.subscriberID);
    }

    async close(): Promise<void> {
        await Promise.allSettled([this.stopPublishing(), this.unsubscribe()]);
    }

    dispose(): void {
        const pending = this.pendingPublisher;
        const publisher = this.publisher;
        const viewer = this.viewer;
        this.pendingPublisher = undefined;
        this.publisher = undefined;
        this.viewer = undefined;
        pending?.capture.getTracks().forEach((track) => track.stop());
        pending?.pc.close();
        publisher?.capture.getTracks().forEach((track) => track.stop());
        publisher?.pc.close();
        viewer?.pc.close();
        this.publisherStats.reset();
        this.viewerStats.reset();
    }

    async getViewerStats(): Promise<ScreenStats | null> {
        return this.viewer ? this.viewerStats.collect(await this.viewer.pc.getStats(), "inbound-rtp") : null;
    }

    async getPublisherStats(): Promise<ScreenStats | null> {
        return this.publisher ? this.publisherStats.collect(await this.publisher.pc.getStats(), "outbound-rtp") : null;
    }
}
