import {useEffect, useRef, useState} from "react";
import type {ConnectionStatsDTO} from "../../api";
import {desktopAPI} from "../../api";
import type {ScreenMediaController} from "../screen/screenMedia";
import type {ScreenSharingState} from "../screen/ScreenShareDialog";
import type {ScreenStats} from "../screen/screenStats";
import {useLocale} from "../../locales/Locale";

export function ConnectionStatsPopup({sessionId, status, channelId, sharing, screenMedia}: {
    sessionId: string; status: string; channelId: string; sharing: ScreenSharingState; screenMedia: ScreenMediaController;
}) {
    const {m} = useLocale();
    const [open, setOpen] = useState(false);
    const [stats, setStats] = useState<ConnectionStatsDTO | null>(null);
    const buttonRef = useRef<HTMLButtonElement>(null);
    const popupRef = useRef<HTMLDivElement>(null);
    const pollId = useRef(0);
    const [screenStats, setScreenStats] = useState<ScreenStats | null>(null);
    useEffect(() => {
        setScreenStats(null);
        if (!open || !sharing.active || status !== "connected") return;
        let active = true;
        let busy = false;
        const request = async () => {
            if (busy) return;
            busy = true;
            try {
                const next = await screenMedia.getPublisherStats();
                if (active) setScreenStats(next);
            } catch { if (active) setScreenStats(null); }
            finally { busy = false; }
        };
        void request();
        const timer = window.setInterval(() => void request(), 1000);
        return () => { active = false; window.clearInterval(timer); };
    }, [open, sharing.active, status, sessionId, channelId, screenMedia]);

    useEffect(() => {
        if (status === "disconnected") setOpen(false);
        setStats(null);
    }, [sessionId, status]);

    useEffect(() => {
        if (!open) return;
        let active = true;
        let busy = false;
        const request = async () => {
            if (busy) return;
            busy = true;
            const id = ++pollId.current;
            try {
                const next = await desktopAPI.connectionStats();
                if (!active || id !== pollId.current) return;
                if (next.sessionId !== sessionId || next.status !== status || status !== "connected") {
                    setStats(null);
                    return;
                }
                setStats(next);
            } catch {
                if (active) setStats(null);
            } finally { busy = false; }
        };
        void request();
        const timer = window.setInterval(() => void request(), 1000);
        return () => { active = false; window.clearInterval(timer); pollId.current++; };
    }, [open, sessionId, status]);

    useEffect(() => {
        if (!open) return;
        const outside = (event: PointerEvent) => {
            const target = event.target as Node;
            if (!buttonRef.current?.contains(target) && !popupRef.current?.contains(target)) setOpen(false);
        };
        const escape = (event: KeyboardEvent) => {
            if (event.key === "Escape") { setOpen(false); buttonRef.current?.focus(); }
        };
        document.addEventListener("pointerdown", outside);
        document.addEventListener("keydown", escape);
        return () => { document.removeEventListener("pointerdown", outside); document.removeEventListener("keydown", escape); };
    }, [open]);

    const statusLabel = m.connection.status[status as keyof typeof m.connection.status] ?? status;
    const ping = stats?.pingAvailable ? m.connection.pingValue(stats.pingMs.toFixed(0), stats.pingVariationAvailable ? stats.pingVariationMs.toFixed(1) : "") : "—";
    return <div className="connection-stats-anchor">
        <button ref={buttonRef} type="button" className={`status-pill ${status}`} aria-label={m.connection.statsButton(statusLabel)}
                aria-expanded={open} aria-controls="connection-stats-popup" onClick={() => setOpen((value) => !value)}>
            <span className="status-dot"/>{statusLabel}
        </button>
        {open && <div ref={popupRef} id="connection-stats-popup" className="connection-stats-popup" role="region" aria-label={m.connection.statsRegion}>
            <h2>{m.connection.statsTitle}</h2>
            <div className="connection-stat-row"><span>{m.connection.lossIn}</span><strong title={stats?.incomingKnown ? undefined : m.connection.noVoiceYet}>{status === "connected" ? `${(stats?.incomingLoss ?? 0).toFixed(1)}%` : "—"}</strong></div>
            <div className="connection-stat-row"><span>{m.connection.recovered}</span><strong title={m.connection.recoveredTitle}>{status === "connected" && stats?.incomingKnown ? `${stats.recoveredLoss.toFixed(1)}%` : "—"}</strong></div>
            <div className="connection-stat-row"><span>{m.connection.plc}</span><strong title={m.connection.plcTitle}>{status === "connected" && stats?.incomingKnown ? `${stats.concealedLoss.toFixed(1)}%` : "—"}</strong></div>
            <div className="connection-stat-row"><span>{m.connection.bursts}</span><strong title={m.connection.burstsTitle}>{status === "connected" && stats?.incomingKnown ? `${stats.lossBurstsSingle} / ${stats.lossBurstsDouble} / ${stats.lossBurstsLong}` : "—"}</strong></div>
            <div className="connection-stat-row"><span>{m.connection.lossOut}</span><strong title={stats?.outgoingKnown ? undefined : m.connection.waitingServer}>{status === "connected" && stats?.outgoingKnown ? `${stats.outgoingLoss.toFixed(1)}%` : "—"}</strong></div>
            <div className="connection-stat-row"><span>{m.connection.ping}</span><strong>{ping}</strong></div>
            {sharing.active && status === "connected" && <section className="connection-screen-stats" aria-label={m.connection.shareStats}>
                <h2>{m.connection.shareTitle}</h2>
                <div className="connection-stat-row"><span>{m.connection.quality}</span><strong>{screenStats?.selected ?? `${sharing.settings.resolution} / ${sharing.settings.fps} FPS / ${sharing.settings.codec}`}</strong></div>
                <div className="connection-stat-row"><span>{m.connection.resolution}</span><strong>{screenStats?.width && screenStats.height ? `${screenStats.width}×${screenStats.height}` : "—"}</strong></div>
                <div className="connection-stat-row"><span>FPS</span><strong>{screenStats ? screenStats.fps.toFixed(0) : "—"}</strong></div>
                <div className="connection-stat-row"><span>{m.connection.bitrate}</span><strong>{screenStats ? m.connection.mbps((screenStats.bitrate / 1_000_000).toFixed(2)) : "—"}</strong></div>
                <div className="connection-stat-row"><span>{m.connection.framesKeys}</span><strong>{screenStats ? `${screenStats.frames} / ${screenStats.keyFrames}` : "—"}</strong></div>
                <div className="connection-stat-row"><span>{m.screen.settings.codec}</span><strong>{screenStats?.codec || "?"}</strong></div>
                {screenStats?.implementation && <div className="connection-stat-row"><span>{m.screen.settings.encoder}</span><strong>{screenStats.implementation}</strong></div>}
                {screenStats?.qualityLimitation && <div className="connection-stat-row"><span>{m.connection.qualityLimit}</span><strong>{screenStats.qualityLimitation}</strong></div>}
            </section>}
        </div>}
    </div>;
}
