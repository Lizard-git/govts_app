import {useEffect, useRef, useState} from "react";
import {Window as WailsWindow} from "@wailsio/runtime";
import type {ClientViewDTO, ParticipantDTO} from "../../api";
import {desktopAPI} from "../../api";
import {ScreenMediaController} from "./screenMedia";
import {defaultScreenProfile, screenProfiles, type ScreenProfileID} from "./screenProfiles";
import type {ScreenStats} from "./screenStats";

function errorText(error: unknown): string {
    return (error instanceof Error ? error.message : String(error)).replace(/^Error:\s*/, "");
}

function screenPickerCancelled(error: unknown): boolean {
    if (error instanceof DOMException && (error.name === "NotAllowedError" || error.name === "AbortError")) return true;
    return /permission denied by user|user cancelled|user canceled/i.test(errorText(error));
}

function bitrateText(value: number): string {
    return value >= 1_000_000 ? `${(value / 1_000_000).toFixed(2)} Мбит/с` : `${Math.round(value / 1000)} Кбит/с`;
}

export function ScreenViewerWindow({streamID, ownerName}: {streamID: string; ownerName: string}) {
    const videoRef = useRef<HTMLVideoElement>(null);
    const controller = useRef<ScreenMediaController | null>(null);
    const [status, setStatus] = useState("Подключение…");
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
                setStatus("Просмотр завершён: вы покинули канал");
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
                setStatus("В эфире");
            } catch (reason) {
                if (!cancelled) { setError(errorText(reason)); setStatus("Ошибка подключения"); }
            }
        })();
        const offState = desktopAPI.onStateChanged(() => void validateMembership().catch((reason) => setError(errorText(reason))));
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
    return <main className="screen-viewer-window">
        <header className="screen-viewer-toolbar">
            <div><strong>{ownerName}</strong><span>{status}{resolution ? ` · ${resolution}` : ""}
                {stats ? ` · ${stats.fps.toFixed(0)} FPS · ${bitrateText(stats.bitrate)} · потеряно ${stats.packetsLost} (${stats.lossSampleAvailable ? `${stats.lossPercent.toFixed(1)}%` : "нет данных"}) · RTT ${stats.rttMs.toFixed(0)} мс · jitter ${stats.jitterMs.toFixed(0)} мс · кадры ${stats.frames}, сброшено ${stats.framesDropped}${stats.freezeCount ? ` · зависания ${stats.freezeCount} / ${(stats.freezeDurationMs / 1000).toFixed(1)} с` : ""}` : ""}</span></div>
            <button onClick={() => void toggleFullscreen()}>{fullscreen ? "Восстановить окно" : "Во весь экран"}</button>
            <button onClick={() => void close()}>Закрыть</button>
        </header>
        {error && <div className="screen-viewer-error" role="alert">{error}</div>}
        <video ref={videoRef} autoPlay playsInline onDoubleClick={() => void toggleFullscreen()} onLoadedMetadata={(event) => {
            const video = event.currentTarget;
            setResolution(`${video.videoWidth}×${video.videoHeight}`);
        }}/>
    </main>;
}

export function ScreenSharing({view, channelID, participants, controller}: {
    view: ClientViewDTO;
    channelID: string;
    participants: ParticipantDTO[];
    controller: ScreenMediaController;
}) {
    const [publishing, setPublishing] = useState(false);
    const [pending, setPending] = useState(false);
    const [error, setError] = useState("");
    const [profileID, setProfileID] = useState<ScreenProfileID>(() => {
        const saved = localStorage.getItem("govts.screenProfile");
        return screenProfiles.some((item) => item.id === saved) ? saved as ScreenProfileID : defaultScreenProfile.id;
    });
    const [publisherStats, setPublisherStats] = useState<ScreenStats | null>(null);
    const streams = (view.screenStreams ?? []).filter((stream) => stream.channelId === channelID);
    const own = streams.find((stream) => stream.ownerSessionId === view.sessionId);
    const profile = screenProfiles.find((item) => item.id === profileID) ?? defaultScreenProfile;

    useEffect(() => {
        if (!publishing && !own) { setPublisherStats(null); return; }
        const timer = window.setInterval(() => void controller.getPublisherStats().then(setPublisherStats).catch(() => undefined), 1000);
        return () => window.clearInterval(timer);
    }, [controller, own, publishing]);

    const start = async () => {
        setPending(true); setError("");
        try {
            await controller.publish(() => setPublishing(false), profile);
            setPublishing(true);
        } catch (reason) { if (!screenPickerCancelled(reason)) setError(errorText(reason)); }
        finally { setPending(false); }
    };
    const stop = async () => {
        setPending(true); setError("");
        try { await controller.stopPublishing(); setPublishing(false); }
        catch (reason) { setError(errorText(reason)); }
        finally { setPending(false); }
    };
    const watch = async (streamID: string, ownerName: string) => {
        setPending(true); setError("");
        try { await desktopAPI.openScreenWindow(streamID, ownerName); }
        catch (reason) { setError(errorText(reason)); }
        finally { setPending(false); }
    };

    return <section className="screen-sharing">
        <div className="screen-heading"><div><span>Демонстрации экрана</span></div>
            <label className="screen-profile"><span>Качество</span><select value={profileID}
                disabled={pending || publishing || Boolean(own)} onChange={(event) => {
                    const value = event.target.value as ScreenProfileID;
                    setProfileID(value);
                    localStorage.setItem("govts.screenProfile", value);
                }}>{screenProfiles.map((item) => <option key={item.id} value={item.id}>{item.label}</option>)}</select></label>
            {channelID === view.channelId && (publishing || own
                ? <button disabled={pending} onClick={() => void stop()}>Завершить</button>
                : <button className="screen-start" disabled={pending} onClick={() => void start()}>Показать экран</button>)}</div>
        {error && <div className="screen-error" role="alert">{error}</div>}
        {publisherStats && <div className="screen-publisher-stats">Отправка: {publisherStats.width || profile.width}×{publisherStats.height || profile.height} · {publisherStats.fps.toFixed(0)} FPS · {bitrateText(publisherStats.bitrate)} · кадры {publisherStats.frames}, ключевые {publisherStats.keyFrames}{publisherStats.qualityLimitation ? ` · limit: ${publisherStats.qualityLimitation}` : ""}</div>}
        {streams.length ? <div className="screen-cards">{streams.map((stream) => {
            const owner = participants.find((item) => item.sessionId === stream.ownerSessionId);
            const ownerName = owner?.displayName ?? "Участник";
            return <article className="screen-card" key={stream.id}>
                <span className="screen-icon" aria-hidden="true">▣</span>
                <div><strong>{ownerName}</strong><small>показывает экран</small></div>
                {stream.ownerSessionId === view.sessionId ? <span className="screen-own">Вы</span>
                    : <button disabled={pending || channelID !== view.channelId} onClick={() => void watch(stream.id, ownerName)}>Смотреть</button>}
            </article>;
        })}</div> : <p className="screen-empty">Сейчас никто не демонстрирует экран.</p>}
    </section>;
}
