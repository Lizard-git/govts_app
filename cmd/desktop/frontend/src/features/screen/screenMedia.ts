import {desktopAPI, logDiagnostic} from "../../api";
import {errorCode, mediaKeyError} from "../../locales";
import {ScreenStatsCollector, type ScreenStats} from "./screenStats";
import {defaultScreenSettings, profileFromSettings, type ScreenSettings} from "./screenProfiles";

type ServerCodecs = {legacy?: boolean; codecs?: {mimeType: string; sdpFmtpLine: string; payloadType: number}[]};
type PublisherState = {streamID: string; pc: RTCPeerConnection; capture: MediaStream; settings: ScreenSettings; cleanup?: () => void};
const fmtp = (line = "") => Object.fromEntries(line.split(";").map((part) => part.trim().split("=")));
const h264Limits: Record<number, [number, number]> = {
    31: [3600, 108000], 32: [5120, 216000], 40: [8192, 245760], 41: [8192, 245760],
    42: [8704, 522240], 50: [22080, 589824], 51: [36864, 983040], 52: [36864, 2073600],
};
function h264Fits(level: number, actual: MediaTrackSettings, fps: number): boolean {
    const limit = h264Limits[level];
    if (!limit || !actual.width || !actual.height) return false;
    const blocks = Math.ceil(actual.width / 16) * Math.ceil(actual.height / 16);
    return blocks <= limit[0] && blocks * Math.min(fps, actual.frameRate ?? fps) <= limit[1];
}
export function senderCodecAvailable(name: string): boolean {
    return Boolean(RTCRtpSender.getCapabilities?.("video")?.codecs.some((c) => c.mimeType.toLowerCase() === `video/${name.toLowerCase()}`));
}
function preferCodecs(transceiver: RTCRtpTransceiver, settings: ScreenSettings, server: ServerCodecs, actual: MediaTrackSettings) {
    const capabilities = RTCRtpSender.getCapabilities?.("video");
    if (!capabilities || !transceiver.setCodecPreferences) {
        if (settings.codec !== "auto" && settings.codec !== "VP8") throw new Error("Codec selection unavailable. Choose VP8 or auto.");
        return;
    }
    const supported = server.legacy ? [{mimeType: "video/VP8", sdpFmtpLine: ""}] : server.codecs ?? [];
    const compatible = capabilities.codecs.filter((c) => supported.some((remote) => {
        if (c.mimeType.toLowerCase() !== remote.mimeType.toLowerCase()) return false;
        const local = fmtp(c.sdpFmtpLine), peer = fmtp(remote.sdpFmtpLine);
        if (/h264$/i.test(c.mimeType)) return local["packetization-mode"] === peer["packetization-mode"] && local["profile-level-id"]?.slice(0,4).toLowerCase() === peer["profile-level-id"]?.slice(0,4).toLowerCase();
        if (/vp9$/i.test(c.mimeType)) return (local["profile-id"] ?? "0") === (peer["profile-id"] ?? "0");
        if (/av1$/i.test(c.mimeType)) return (local.profile ?? "0") === (peer.profile ?? "0");
        return !/rtx$/i.test(c.mimeType);
    }));
    const selected = compatible.filter((c) => settings.codec === "auto" || c.mimeType.toLowerCase() === `video/${settings.codec.toLowerCase()}`);
    // Keep VP8 first in auto until quality and CPU comparisons are available.
    selected.sort((a,b) => Number(!/vp8$/i.test(a.mimeType))-Number(!/vp8$/i.test(b.mimeType)));
    if (!selected.length) throw new Error(`${settings.codec} is unavailable in this client/server. Choose VP8 or auto.`);
    if (selected.some((c) => /h264$/i.test(c.mimeType))) {
        const valid = selected.filter((c) => {
            if (!/h264$/i.test(c.mimeType)) return true;
            const local = fmtp(c.sdpFmtpLine);
            const level = parseInt(local["profile-level-id"]?.slice(4) ?? "0", 16);
            return supported.some((remote) => {
                const peer = fmtp(remote.sdpFmtpLine);
                if (!/h264$/i.test(remote.mimeType) || local["packetization-mode"] !== peer["packetization-mode"] || local["profile-level-id"]?.slice(0,4).toLowerCase() !== peer["profile-level-id"]?.slice(0,4).toLowerCase()) return false;
                const peerLevel = parseInt(peer["profile-level-id"]?.slice(4) ?? "0", 16);
                return h264Fits(Math.min(level, peerLevel), actual, settings.fps);
            });
        });
        if (!valid.length) throw new Error("H.264 profile/level cannot support the captured resolution and FPS. Lower them or choose VP8, VP9 or AV1.");
        selected.splice(0,selected.length,...valid);
    }
    transceiver.setCodecPreferences([...selected, ...capabilities.codecs.filter((c) => /rtx$/i.test(c.mimeType))]);
}
function verifyNegotiatedCodec(sdp: string, settings: ScreenSettings, actual: MediaTrackSettings) {
    const video = sdp.split(/(?=m=)/).find((section) => section.startsWith("m=video "));
    const payload = video?.split(/\r?\n/)[0]?.split(" ")[3];
    const codec = video?.match(new RegExp(`a=rtpmap:${payload} ([^/]+)/`, "i"))?.[1];
    if (!codec || video?.startsWith("m=video 0 ")) throw new Error("Server rejected the video codec. Choose VP8 or auto.");
    if (settings.codec !== "auto" && codec.toLowerCase() !== settings.codec.toLowerCase()) throw new Error(`Requested ${settings.codec}, negotiated ${codec}. Choose a supported codec.`);
    const parameters = fmtp(video?.match(new RegExp(`a=fmtp:${payload} ([^\r\n]+)`, "i"))?.[1]);
    if (codec.toLowerCase() === "h264" && !h264Fits(parseInt(parameters["profile-level-id"]?.slice(4) ?? "0", 16), actual, settings.fps)) {
        throw new Error("Negotiated H.264 profile/level cannot support the captured resolution and FPS.");
    }
    logDiagnostic("screen_codec_negotiated", codec);
}

function waitForGathering(pc: RTCPeerConnection): Promise<void> {
    if (pc.connectionState === "closed") return Promise.reject(new DOMException(errorCode("screen-aborted"), "AbortError"));
    if (pc.iceGatheringState === "complete") return Promise.resolve();
    return new Promise((resolve, reject) => {
        const timeout = window.setTimeout(() => finish(new Error(errorCode("screen-offer-timeout"))), 10_000);
        const changed = () => {
            if (pc.iceGatheringState === "complete") finish();
            else if (pc.connectionState === "closed") finish(new DOMException(errorCode("screen-aborted"), "AbortError"));
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
    if (!pc.localDescription?.sdp) throw new Error(errorCode("screen-offer-failed"));
    return {type: "offer", sdp: pc.localDescription.sdp};
}

function waitForConnected(pc: RTCPeerConnection, timeoutMs = 20_000): Promise<void> {
    if (pc.connectionState === "connected") return Promise.resolve();
    if (pc.connectionState === "closed" || pc.connectionState === "failed") return Promise.reject(new Error(errorCode("screen-connect-closed")));
    return new Promise((resolve, reject) => {
        const timeout = window.setTimeout(() => finish(new Error(errorCode("screen-connect-failed"))), timeoutMs);
        const changed = () => {
            if (pc.connectionState === "connected") finish();
            if (pc.connectionState === "failed" || pc.connectionState === "closed") {
                finish(new Error(errorCode("screen-connect-closed")));
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

function waitForRemoteVideo(pc: RTCPeerConnection, timeoutMs = 20_000): Promise<MediaStream> {
    if (pc.connectionState === "closed" || pc.connectionState === "failed") return Promise.reject(new Error(errorCode("screen-video-closed")));
    return new Promise((resolve, reject) => {
        const timeout = window.setTimeout(() => finish(undefined, new Error(errorCode("screen-video-timeout"))), timeoutMs);
        const onTrack = (event: RTCTrackEvent) => {
            if (event.track.kind !== "video") return;
            finish(event.streams[0] ?? new MediaStream([event.track]));
        };
        const onConnectionStateChanged = () => {
            if (pc.connectionState === "failed" || pc.connectionState === "closed") {
                finish(undefined, new Error(errorCode("screen-video-closed")));
            }
        };
        const finish = (stream?: MediaStream, error?: Error) => {
            window.clearTimeout(timeout);
            pc.removeEventListener("track", onTrack);
            pc.removeEventListener("connectionstatechange", onConnectionStateChanged);
            if (error) reject(error);
            else if (stream) resolve(stream);
        };
        pc.addEventListener("track", onTrack);
        pc.addEventListener("connectionstatechange", onConnectionStateChanged);
    });
}

async function ensureTrusted(): Promise<void> {
    const identity = await desktopAPI.mediaServerIdentity();
    if (identity.trusted) return;
    if (identity.known) throw new Error(mediaKeyError(identity.fingerprint));
    await desktopAPI.trustMediaServer(identity.fingerprint);
}

export class ScreenMediaController {
    private publisher?: PublisherState;
    private pendingPublisher?: {pc: RTCPeerConnection; capture: MediaStream};
    private viewerGeneration = 0;
    private pendingViewer?: RTCPeerConnection;
    private viewer?: {streamID: string; subscriberID: string; pc: RTCPeerConnection};
    private publisherStats = new ScreenStatsCollector();
    private viewerStats = new ScreenStatsCollector();
    private publishGeneration = 0;
    private picking = false;

    async publish(onEnded: () => void, settings: ScreenSettings = defaultScreenSettings, isCurrent: () => boolean = () => true): Promise<string> {
        if (this.publisher) return this.publisher.streamID;
        if (this.picking || this.pendingPublisher) throw new Error(errorCode("screen-already-starting"));
        settings = {...settings};
        const profile = profileFromSettings(settings);
        const generation = ++this.publishGeneration;
        const checkCurrent = () => {
            if (generation !== this.publishGeneration || !isCurrent()) throw new DOMException(errorCode("screen-aborted"), "AbortError");
        };
        // Keep the picker in the direct click call chain: some WebViews require user activation.
        this.picking = true;
        let capture: MediaStream;
        try {
            logDiagnostic("screen_picker_open", `generation=${generation}`);
            capture = await navigator.mediaDevices.getDisplayMedia({video: true, audio: false});
            logDiagnostic("screen_picker_selected", `generation=${generation}`);
        } catch (error) {
            logDiagnostic("screen_picker_closed", error);
            throw error;
        }
        finally { this.picking = false; }
        try { checkCurrent(); }
        catch (error) { capture.getTracks().forEach((track) => track.stop()); throw error; }
        let pc: RTCPeerConnection;
        try { pc = new RTCPeerConnection({iceServers: []}); }
        catch (error) { capture.getTracks().forEach((track) => track.stop()); throw error; }
        const pending = {pc, capture};
        this.pendingPublisher = pending;
        const requireCaptureTrack = (track: MediaStreamTrack | undefined): MediaStreamTrack => {
            if (!track) throw new Error(errorCode("screen-no-track"));
            return track;
        };
        const ensureStillPending = () => {
            if (this.pendingPublisher !== pending) throw new DOMException(errorCode("screen-aborted"), "AbortError");
        };
        let streamID = "";
        try {
            const captureTrack = requireCaptureTrack(capture.getVideoTracks()[0]);
            const checkCapture = () => {
                if (captureTrack.readyState === "ended") throw new DOMException(errorCode("screen-capture-ended"), "AbortError");
            };
            captureTrack.onended = () => pc.close();
            captureTrack.contentHint = profile.contentHint;
            const source = captureTrack.getSettings();
            const scale = profile.width && source.width && source.height ? Math.min(1, profile.width/source.width, profile.height/source.height) : 1;
            const constraints: MediaTrackConstraints = {frameRate: {ideal: profile.frameRate, max: profile.frameRate}};
            if (profile.width) {
                constraints.width = {max: Math.floor((source.width ?? profile.width)*scale)};
                constraints.height = {max: Math.floor((source.height ?? profile.height)*scale)};
            }
            await captureTrack.applyConstraints(constraints).catch((error) => logDiagnostic("screen_capture_constraints_fallback", error));
            logDiagnostic("screen_capture_actual", JSON.stringify(captureTrack.getSettings()));
            checkCurrent();
            const transceiver = pc.addTransceiver(captureTrack, {
                direction: "sendonly",
                streams: [capture],
                sendEncodings: [{maxBitrate: profile.bitrate, maxFramerate: profile.frameRate}],
            });
            const senderParameters = transceiver.sender.getParameters();
            senderParameters.degradationPreference = profile.degradationPreference;
            await transceiver.sender.setParameters(senderParameters).catch((error) => { logDiagnostic("screen_sender_parameters", error); });
            await ensureTrusted();
            checkCurrent();
            const server = await desktopAPI.screenCapabilities();
            checkCurrent();
            preferCodecs(transceiver, settings, server, captureTrack.getSettings());
            checkCurrent();
            const offer = await localOffer(pc);
            checkCurrent();
            checkCapture();
            const result = await desktopAPI.publishScreen(offer);
            streamID = result.streamId;
            checkCurrent();
            await pc.setRemoteDescription({type: "answer", sdp: result.answer.sdp});
            verifyNegotiatedCodec(result.answer.sdp, settings, captureTrack.getSettings());
            await waitForConnected(pc);
            checkCurrent();
            checkCapture();
            ensureStillPending();
            this.pendingPublisher = undefined;
            const active: PublisherState = {streamID, pc, capture, settings: {...settings}};
            this.publisher = active;
            let disconnectedTimer: number | undefined;
            let disconnectExpired = false;
            const connectionChanged = () => {
                logDiagnostic("screen_publisher_state", `stream=${streamID} connection=${pc.connectionState} ice=${pc.iceConnectionState}`);
                window.clearTimeout(disconnectedTimer);
                disconnectedTimer = undefined;
                if (pc.connectionState === "disconnected" && !disconnectExpired) {
                    // A short ICE interruption can recover. Do not tear down a
                    // healthy capture unless the disconnect persists.
                    disconnectedTimer = window.setTimeout(() => {
                        disconnectExpired = true;
                        connectionChanged();
                    }, 5_000);
                    return;
                }
                if (pc.connectionState !== "failed" && pc.connectionState !== "closed" &&
                    !(disconnectExpired && pc.connectionState === "disconnected")) return;
                if (this.publisher !== active) return;
                this.publisher = undefined;
                this.publisherStats.reset();
                capture.getTracks().forEach((track) => track.stop());
                active.cleanup?.();
                if (pc.connectionState !== "closed") pc.close();
                void desktopAPI.stopScreen(streamID).catch((error) => { logDiagnostic("screen_stop_cleanup", error); });
                onEnded();
            };
            active.cleanup = () => {
                window.clearTimeout(disconnectedTimer);
                pc.removeEventListener("connectionstatechange", connectionChanged);
                captureTrack.onended = null;
            };
            pc.addEventListener("connectionstatechange", connectionChanged);
            captureTrack.onended = () => void this.stopPublishing().catch((error) => logDiagnostic("screen_stop_cleanup", error)).finally(onEnded);
            return result.streamId;
        } catch (error) {
            logDiagnostic("screen_publish_failed", error);
            if (this.pendingPublisher === pending) this.pendingPublisher = undefined;
            capture.getTracks().forEach((track) => track.stop());
            pc.close();
            if (streamID) await desktopAPI.stopScreen(streamID).catch((error) => { logDiagnostic("screen_publish_cleanup", error); });
            checkCurrent();
            throw error;
        }
    }

    async stopPublishing(): Promise<void> {
        ++this.publishGeneration;
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
        active.cleanup?.();
        active.capture.getTracks().forEach((track) => track.stop());
        active.pc.close();
        await desktopAPI.stopScreen(active.streamID);
    }

    async subscribe(streamID: string): Promise<MediaStream> {
        const cleanup = this.unsubscribe();
        const generation = this.viewerGeneration;
        const checkCurrent = () => {
            if (generation !== this.viewerGeneration) throw new DOMException(errorCode("screen-aborted"), "AbortError");
        };
        await cleanup;
        checkCurrent();
        await ensureTrusted();
        checkCurrent();
        const pc = new RTCPeerConnection({iceServers: []});
        this.pendingViewer = pc;
        pc.addEventListener("connectionstatechange", () => {
            logDiagnostic("screen_viewer_state", `stream=${streamID} connection=${pc.connectionState} ice=${pc.iceConnectionState}`);
        });
        // Install the listener before applying the answer: WebRTC dispatches `track`
        // from setRemoteDescription, before the signaling call below returns.
        const remoteVideo = waitForRemoteVideo(pc);
        void remoteVideo.catch(() => undefined);
        const subscriberID = crypto.randomUUID();
        try {
            const receiver = pc.addTransceiver("video", {direction: "recvonly"});
            const receiveCodecs = RTCRtpReceiver.getCapabilities?.("video")?.codecs;
            if (receiveCodecs && receiver.setCodecPreferences) receiver.setCodecPreferences(receiveCodecs);
            const result = await desktopAPI.subscribeScreen(streamID, subscriberID, await localOffer(pc));
            await pc.setRemoteDescription({type: "answer", sdp: result.answer.sdp});
            const remote = await remoteVideo;
            await waitForConnected(pc);
            if (generation !== this.viewerGeneration) throw new DOMException(errorCode("screen-aborted"), "AbortError");
            this.pendingViewer = undefined;
            this.viewer = {streamID, subscriberID, pc};
            this.viewerStats.reset();
            return remote;
        } catch (error) {
            if (this.pendingViewer === pc) this.pendingViewer = undefined;
            logDiagnostic("screen_subscribe_failed", error);
            pc.close();
            await desktopAPI.unsubscribeScreen(streamID, subscriberID).catch((reason) => logDiagnostic("screen_subscribe_cleanup", reason));
            checkCurrent();
            throw error;
        }
    }

    async unsubscribe(): Promise<void> {
        ++this.viewerGeneration;
        this.pendingViewer?.close();
        this.pendingViewer = undefined;
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
        ++this.viewerGeneration;
        this.pendingViewer?.close();
        this.pendingViewer = undefined;
        ++this.publishGeneration;
        const pending = this.pendingPublisher;
        const publisher = this.publisher;
        const viewer = this.viewer;
        this.pendingPublisher = undefined;
        this.publisher = undefined;
        this.viewer = undefined;
        pending?.capture.getTracks().forEach((track) => track.stop());
        pending?.pc.close();
        publisher?.capture.getTracks().forEach((track) => track.stop());
        publisher?.cleanup?.();
        publisher?.pc.close();
        viewer?.pc.close();
        if (publisher) void desktopAPI.stopScreen(publisher.streamID).catch((error) => logDiagnostic("screen_dispose_cleanup", error));
        if (viewer) void desktopAPI.unsubscribeScreen(viewer.streamID, viewer.subscriberID).catch((error) => logDiagnostic("screen_dispose_cleanup", error));
        this.publisherStats.reset();
        this.viewerStats.reset();
    }

    async getViewerStats(): Promise<ScreenStats | null> {
        return this.viewer ? this.viewerStats.collect(await this.viewer.pc.getStats(), "inbound-rtp") : null;
    }

    async getPublisherStats(): Promise<ScreenStats | null> {
        const active = this.publisher;
        if (!active) return null;
        const stats = this.publisherStats.collect(await active.pc.getStats(), "outbound-rtp");
        return stats ? {...stats, selected: `${active.settings.resolution} / ${active.settings.fps} FPS / ${active.settings.codec}`} : null;
    }
}
