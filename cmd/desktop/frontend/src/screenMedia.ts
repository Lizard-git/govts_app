import {desktopAPI} from "./api";

function waitForGathering(pc: RTCPeerConnection): Promise<void> {
    if (pc.iceGatheringState === "complete") return Promise.resolve();
    return new Promise((resolve) => {
        const changed = () => {
            if (pc.iceGatheringState !== "complete") return;
            pc.removeEventListener("icegatheringstatechange", changed);
            resolve();
        };
        pc.addEventListener("icegatheringstatechange", changed);
    });
}

async function localOffer(pc: RTCPeerConnection): Promise<{type: string; sdp: string}> {
    await pc.setLocalDescription(await pc.createOffer());
    await waitForGathering(pc);
    if (!pc.localDescription?.sdp) throw new Error("Не удалось создать WebRTC offer");
    return {type: "offer", sdp: pc.localDescription.sdp};
}

async function ensureTrusted(): Promise<void> {
    const identity = await desktopAPI.mediaServerIdentity();
    if (identity.trusted) return;
    if (identity.known) throw new Error(`Ключ media-сервера изменился. Новый отпечаток: ${identity.fingerprint}`);
    await desktopAPI.trustMediaServer(identity.fingerprint);
}

export class ScreenMediaController {
    private publisher?: {streamID: string; pc: RTCPeerConnection; capture: MediaStream};
    private viewer?: {streamID: string; subscriberID: string; pc: RTCPeerConnection};

    async publish(onEnded: () => void): Promise<string> {
        if (this.publisher) return this.publisher.streamID;
        // Keep the picker in the direct click call chain: some WebViews require user activation.
        const capture = await navigator.mediaDevices.getDisplayMedia({video: true, audio: false});
        const pc = new RTCPeerConnection({iceServers: []});
        try {
            for (const track of capture.getVideoTracks()) pc.addTrack(track, capture);
            await ensureTrusted();
            const result = await desktopAPI.publishScreen(await localOffer(pc));
            await pc.setRemoteDescription({type: "answer", sdp: result.answer.sdp});
            this.publisher = {streamID: result.streamId, pc, capture};
            const track = capture.getVideoTracks()[0];
            if (track) track.onended = () => void this.stopPublishing().finally(onEnded);
            return result.streamId;
        } catch (error) {
            capture.getTracks().forEach((track) => track.stop());
            pc.close();
            throw error;
        }
    }

    async stopPublishing(): Promise<void> {
        const active = this.publisher;
        this.publisher = undefined;
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
            return remote;
        } catch (error) {
            pc.close();
            throw error;
        }
    }

    async unsubscribe(): Promise<void> {
        const active = this.viewer;
        this.viewer = undefined;
        if (!active) return;
        active.pc.close();
        await desktopAPI.unsubscribeScreen(active.streamID, active.subscriberID);
    }

    async close(): Promise<void> {
        await Promise.allSettled([this.stopPublishing(), this.unsubscribe()]);
    }
}
