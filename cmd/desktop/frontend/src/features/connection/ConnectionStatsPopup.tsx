import {useEffect, useRef, useState} from "react";
import type {ConnectionStatsDTO} from "../../api";
import {desktopAPI} from "../../api";
import type {ScreenMediaController} from "../screen/screenMedia";
import type {ScreenSharingState} from "../screen/ScreenShareDialog";
import type {ScreenStats} from "../screen/screenStats";

export function ConnectionStatsPopup({sessionId, status, channelId, sharing, screenMedia}: {
    sessionId: string; status: string; channelId: string; sharing: ScreenSharingState; screenMedia: ScreenMediaController;
}) {
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

    const labels: Record<string, string> = {connecting: "Подключение", connected: "Подключено", reconnecting: "Переподключение", disconnected: "Отключено"};
    return <div className="connection-stats-anchor">
        <button ref={buttonRef} type="button" className={`status-pill ${status}`} aria-label={`${labels[status] ?? status}. Статистика подключения`}
                aria-expanded={open} aria-controls="connection-stats-popup" onClick={() => setOpen((value) => !value)}>
            <span className="status-dot"/>{labels[status] ?? status}
        </button>
        {open && <div ref={popupRef} id="connection-stats-popup" className="connection-stats-popup" role="region" aria-label="Статус подключения">
            <h2>Статус подключения</h2>
            <div className="connection-stat-row"><span>Потеря пакетов (входящие)</span><strong title={stats?.incomingKnown ? undefined : "Голосовых пакетов пока нет"}>{status === "connected" ? `${(stats?.incomingLoss ?? 0).toFixed(1)}%` : "—"}</strong></div>
            <div className="connection-stat-row"><span>Потеря пакетов (исходящие)</span><strong title={stats?.outgoingKnown ? undefined : "Ожидание данных сервера"}>{status === "connected" && stats?.outgoingKnown ? `${stats.outgoingLoss.toFixed(1)}%` : "—"}</strong></div>
            <div className="connection-stat-row"><span>Пинг</span><strong>{stats?.pingAvailable ? `${stats.pingMs.toFixed(0)} мс${stats.pingVariationAvailable ? ` ± ${stats.pingVariationMs.toFixed(1)}` : ""}` : "—"}</strong></div>
            {sharing.active && status === "connected" && <section className="connection-screen-stats" aria-label="Статистика демонстрации">
                <h2>Демонстрация экрана</h2>
                <div className="connection-stat-row"><span>Качество</span><strong>{sharing.profile.label}</strong></div>
                <div className="connection-stat-row"><span>Разрешение</span><strong>{screenStats?.width && screenStats.height ? `${screenStats.width}×${screenStats.height}` : "—"}</strong></div>
                <div className="connection-stat-row"><span>FPS</span><strong>{screenStats ? screenStats.fps.toFixed(0) : "—"}</strong></div>
                <div className="connection-stat-row"><span>Битрейт</span><strong>{screenStats ? `${(screenStats.bitrate / 1_000_000).toFixed(2)} Мбит/с` : "—"}</strong></div>
                <div className="connection-stat-row"><span>Кадры / ключевые</span><strong>{screenStats ? `${screenStats.frames} / ${screenStats.keyFrames}` : "—"}</strong></div>
                {screenStats?.qualityLimitation && <div className="connection-stat-row"><span>Ограничение качества</span><strong>{screenStats.qualityLimitation}</strong></div>}
            </section>}
        </div>}
    </div>;
}
