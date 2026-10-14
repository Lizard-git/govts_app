import {desktopAPI, logDiagnostic} from "../../api";
import {errorCode, mediaKeyError} from "../../locales";
import {ScreenStatsCollector, type ScreenStats} from "./screenStats";
import {defaultScreenProfile, type ScreenProfile} from "./screenProfiles";

function waitForGathering(pc: RTCPeerConnection): Promise<void> {
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

function peerClosed(pc: RTCPeerConnection): boolean {
    return pc.signalingState === "closed" || pc.connectionState === "closed";
}

async function ensureTrusted(): Promise<void> {
    const identity = await desktopAPI.mediaServerIdentity();
    if (identity.trusted) return;
    if (identity.known) throw new Error(mediaKeyError(identity.fingerprint));
    await desktopAPI.trustMediaServer(identity.fingerprint);
}

export class ScreenMediaController {
    private publisher?: {streamID: string; pc: RTCPeerConnection; capture: MediaStream};
    private pendingPublisher?: {pc: RTCPeerConnection; capture: MediaStream};
    private viewer?: {streamID: string; subscriberID: string; pc: RTCPeerConnection};
    private publisherStats = new ScreenStatsCollector();
    private viewerStats = new ScreenStatsCollector();
    private publishGeneration = 0;
    private picking = false;

    async publish(onEnded: () => void, profile: ScreenProfile = defaultScreenProfile, isCurrent: () => boolean = () => true): Promise<string> {
        if (this.publisher) return this.publisher.streamID;
        if (this.picking || this.pendingPublisher) throw new Error(errorCode("screen-already-starting"));
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
        const pc = new RTCPeerConnection({iceServers: []});
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
            let captureEnded = false;
            captureTrack.onended = () => { captureEnded = true; };
            const ensureCaptureOpen = () => {
                if (captureEnded || captureTrack.readyState === "ended" || pc.signalingState === "closed" || pc.connectionState === "closed") {
                    throw new DOMException(errorCode("screen-aborted"), "AbortError");
                }
            };
            captureTrack.contentHint = profile.contentHint;
            try {
                await captureTrack.applyConstraints({
                    width: {ideal: profile.width, max: profile.width},
                    height: {ideal: profile.height, max: profile.height},
                    frameRate: {min: profile.minFrameRate, ideal: profile.frameRate, max: profile.frameRate},
                });
            } catch (error) {
                logDiagnostic("screen_capture_constraints_fallback", error);
                // Some capture backends reject minimum frame-rate constraints.
                // Retain the preferred target without aborting screen sharing.
                await captureTrack.applyConstraints({
                    width: {ideal: profile.width, max: profile.width},
                    height: {ideal: profile.height, max: profile.height},
                    frameRate: {ideal: profile.frameRate, max: profile.frameRate},
                }).catch((error) => { logDiagnostic("screen_capture_constraints", error); });
            }
            checkCurrent();
            ensureCaptureOpen();
            let transceiver: RTCRtpTransceiver;
            try {
                transceiver = pc.addTransceiver(captureTrack, {
                    direction: "sendonly",
                    streams: [capture],
                    sendEncodings: [{maxBitrate: profile.bitrate}],
                });
            } catch (error) {
                if (peerClosed(pc) || captureEnded || captureTrack.readyState === "ended") {
                    throw new DOMException(errorCode("screen-aborted"), "AbortError");
                }
                throw error;
            }
            const senderParameters = transceiver.sender.getParameters();
            senderParameters.degradationPreference = profile.degradationPreference;
            await transceiver.sender.setParameters(senderParameters).catch((error) => { logDiagnostic("screen_sender_parameters", error); });
            await ensureTrusted();
            checkCurrent();
            const offer = await localOffer(pc);
            checkCurrent();
            checkCapture();
            const result = await desktopAPI.publishScreen(offer);
            streamID = result.streamId;
            checkCurrent();
            await pc.setRemoteDescription({type: "answer", sdp: result.answer.sdp});
            await waitForConnected(pc);
            checkCurrent();
            checkCapture();
            ensureStillPending();
            this.pendingPublisher = undefined;
            const active = {streamID, pc, capture};
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
                pc.removeEventListener("connectionstatechange", connectionChanged);
                if (pc.connectionState !== "closed") pc.close();
                void desktopAPI.stopScreen(streamID).catch((error) => { logDiagnostic("screen_stop_cleanup", error); });
                onEnded();
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
            pending.capture.getTracks().forEach((track) => {
                track.onended = null;
                track.stop();
            });
            if (pending.pc.signalingState !== "closed") pending.pc.close();
        }
        if (!active) return;
        active.capture.getTracks().forEach((track) => {
            track.onended = null;
            track.stop();
        });
        if (active.pc.signalingState !== "closed") active.pc.close();
        await desktopAPI.stopScreen(active.streamID);
    }

    async subscribe(streamID: string): Promise<MediaStream> {
        await this.unsubscribe();
        await ensureTrusted();
        const pc = new RTCPeerConnection({iceServers: []});
        pc.addEventListener("connectionstatechange", () => {
            logDiagnostic("screen_viewer_state", `stream=${streamID} connection=${pc.connectionState} ice=${pc.iceConnectionState}`);
        });
        // Install the listener before applying the answer: WebRTC dispatches `track`
        // from setRemoteDescription, before the signaling call below returns.
        const remoteVideo = waitForRemoteVideo(pc);
        void remoteVideo.catch(() => undefined);
        try {
            pc.addTransceiver("video", {direction: "recvonly"});
        } catch (error) {
            if (peerClosed(pc)) throw new DOMException(errorCode("screen-aborted"), "AbortError");
            throw error;
        }
        const subscriberID = crypto.randomUUID();
        try {
            const result = await desktopAPI.subscribeScreen(streamID, subscriberID, await localOffer(pc));
            await pc.setRemoteDescription({type: "answer", sdp: result.answer.sdp});
            const remote = await remoteVideo;
            await waitForConnected(pc);
            this.viewer = {streamID, subscriberID, pc};
            this.viewerStats.reset();
            return remote;
        } catch (error) {
            logDiagnostic("screen_subscribe_failed", error);
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
