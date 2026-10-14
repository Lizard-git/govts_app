import {useEffect, useRef, useState} from "react";
import {Window as WailsWindow} from "@wailsio/runtime";
import type {ClientViewDTO, ParticipantDTO, ScreenStreamDTO} from "../../api";
import {desktopAPI} from "../../api";
import {ScreenMediaController} from "./screenMedia";
import type {ScreenStats} from "./screenStats";
import {Icon} from "../../components/Icon";
import {errorText, rawError} from "../../locales";
import {useLocale} from "../../locales/Locale";

function bitrateText(value: number, m: ReturnType<typeof useLocale>["m"]): string {
    return value >= 1_000_000 ? m.screen.mbps((value / 1_000_000).toFixed(2)) : m.screen.kbps(String(Math.round(value / 1000)));
}

export function ScreenViewerWindow({streamID, ownerName}: {streamID: string; ownerName: string}) {
    const {m} = useLocale();
    const videoRef = useRef<HTMLVideoElement>(null);
    const controller = useRef<ScreenMediaController | null>(null);
    const [status, setStatus] = useState<"connecting" | "left" | "live" | "failed">("connecting");
    const [resolution, setResolution] = useState("");
    const [error, setError] = useState("");
    const [fullscreen, setFullscreen] = useState(false);
    const [stats, setStats] = useState<ScreenStats | null>(null);

    useEffect(() => {
        let cancelled = false;
        const media = new ScreenMediaController();
        controller.current = media;
        const close = async () => {
            await media.unsubscribe();
            if (!cancelled) await WailsWindow.Close();
        };
        const validateMembership = async () => {
            const snapshot = await desktopAPI.snapshot();
            const stream = (snapshot.screenStreams ?? []).find((item) => item.id === streamID);
            if (!stream || stream.channelId !== snapshot.channelId) {
                setStatus("left");
                await close();
                return false;
            }
            return true;
        };
        void (async () => {
            try {
                if (!await validateMembership() || cancelled) return;
                const remote = await media.subscribe(streamID);
                if (cancelled) { await media.unsubscribe(); return; }
                if (videoRef.current) {
                    videoRef.current.srcObject = remote;
                    await videoRef.current.play();
                }
                setStatus("live");
            } catch (reason) {
                if (!cancelled) { setError(rawError(reason)); setStatus("failed"); }
            }
        })();
        const offState = desktopAPI.onStateChanged(() => void validateMembership().catch((reason) => setError(rawError(reason))));
        return () => {
            cancelled = true;
            offState();
            media.dispose();
            controller.current = null;
        };
    }, [streamID]);
    useEffect(() => {
        const timer = window.setInterval(() => void controller.current?.getViewerStats().then(setStats).catch(() => undefined), 1000);
        return () => window.clearInterval(timer);
    }, []);

    const close = async () => {
        await controller.current?.unsubscribe();
        await WailsWindow.Close();
    };
    const toggleFullscreen = async () => {
        await WailsWindow.ToggleFullscreen();
        setFullscreen(await WailsWindow.IsFullscreen());
    };
    const statusText = status === "left" ? m.screen.leftChannel : status === "live" ? m.screen.live : status === "failed" ? m.screen.connectError : m.screen.connecting;
    const statsText = stats ? m.screen.viewerLine(stats.fps.toFixed(0), bitrateText(stats.bitrate, m), stats.packetsLost, stats.lossSampleAvailable ? `${stats.lossPercent.toFixed(1)}%` : m.screen.lossUnknown, stats.rttMs.toFixed(0), stats.jitterMs.toFixed(0), stats.frames, stats.framesDropped) + (stats.freezeCount ? m.screen.freezes(stats.freezeCount, (stats.freezeDurationMs / 1000).toFixed(1)) : "") : "";
    return <main className="screen-viewer-window">
        <header className="screen-viewer-toolbar">
            <div><strong>{ownerName}</strong><span>{statusText}{resolution ? ` · ${resolution}` : ""}{statsText}{stats?.codec ? ` ? ${stats.codec}` : ""}{stats?.implementation ? ` ? ${stats.implementation}` : ""}</span></div>
            <button onClick={() => void toggleFullscreen()}>{fullscreen ? m.screen.restore : m.screen.fullscreen}</button>
            <button onClick={() => void close()}>{m.screen.close}</button>
        </header>
        {error && <div className="screen-viewer-error" role="alert">{errorText(error, m)}</div>}
        <video ref={videoRef} autoPlay muted playsInline onDoubleClick={() => void toggleFullscreen()} onLoadedMetadata={(event) => {
            const video = event.currentTarget;
            setResolution(`${video.videoWidth}×${video.videoHeight}`);
        }}/>
    </main>;
}

export function ScreenStageTile({stream, ownerName, local, connected, onError}: {
    stream: ScreenStreamDTO; ownerName: string; local: boolean; connected: boolean; onError: (message: string) => void;
}) {
    const {m} = useLocale();
    const [pending, setPending] = useState(false);
    const watch = async () => {
        if (pending) return;
        setPending(true);
        try { await desktopAPI.openScreenWindow(stream.id, ownerName); }
        catch (reason) { onError(rawError(reason)); }
        finally { setPending(false); }
    };
    return <div className="stage-screen" aria-label={m.screen.tile(ownerName)}>
        <span className="stage-screen-icon"><Icon name="screen"/></span>
        <span className="stage-screen-name" title={m.screen.tileName(ownerName)}>{m.screen.tileName(ownerName)}</span>
        {local ? <span className="stage-screen-own">{m.screen.you}</span> : <button type="button" disabled={pending || !connected} onClick={() => void watch()}>{m.screen.watch}</button>}
    </div>;
}

export function ScreenSharing({view, channelID, participants}: {
    view: ClientViewDTO;
    channelID: string;
    participants: ParticipantDTO[];
}) {
    const {m} = useLocale();
    const [pending, setPending] = useState(false);
    const [error, setError] = useState("");
    const streams = (view.screenStreams ?? []).filter((stream) => stream.channelId === channelID);
    const watch = async (streamID: string, ownerName: string) => {
        setPending(true); setError("");
        try { await desktopAPI.openScreenWindow(streamID, ownerName); }
        catch (reason) { setError(rawError(reason)); }
        finally { setPending(false); }
    };

    return <section className="screen-sharing">
        <div className="screen-heading"><div><span>{m.screen.list}</span></div></div>
        <div className="screen-list">
        {error && <div className="screen-error" role="alert">{errorText(error, m)}</div>}
        {streams.length ? <div className="screen-cards">{streams.map((stream) => {
            const owner = participants.find((item) => item.sessionId === stream.ownerSessionId);
            const ownerName = owner?.displayName ?? m.screen.participant;
            return <article className="screen-card" key={stream.id}>
                <div className="screen-card-preview" aria-label={m.screen.card(ownerName)}><Icon name="screen"/><span>{ownerName}</span><small>{m.screen.cardKind}</small></div>
                <div><strong>{ownerName}</strong><small>{m.screen.showing}</small></div>
                {stream.ownerSessionId === view.sessionId ? <span className="screen-own">{m.screen.you}</span>
                    : <button disabled={pending || channelID !== view.channelId} onClick={() => void watch(stream.id, ownerName)}>{m.screen.watch}</button>}
            </article>;
        })}</div> : <p className="screen-empty">{m.screen.nobody}</p>}
        </div>
    </section>;
}
