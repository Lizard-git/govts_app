import {useEffect, useRef, useState} from "react";
import type {ConnectionStatsDTO} from "../../api";
import {desktopAPI} from "../../api";

export function ConnectionStatsPopup({sessionId, status}: {sessionId: string; status: string}) {
    const [open, setOpen] = useState(false);
    const [stats, setStats] = useState<ConnectionStatsDTO | null>(null);
    const buttonRef = useRef<HTMLButtonElement>(null);
    const popupRef = useRef<HTMLDivElement>(null);
    const pollId = useRef(0);

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
        </div>}
    </div>;
}
