import {useCallback, useEffect, useRef, useState} from "react";
import {desktopAPI, type ClientViewDTO, type RecentServer} from "../../api";
import {Icon} from "../../components/Icon";

function statusTitle(server: RecentServer): string {
    const label = server.current ? (server.status === "available" ? "Текущее подключение" : "Синхронизация текущего подключения")
        : server.status === "available" ? "Сервер ответил на последнюю проверку"
        : server.status === "unknown" ? "Сервер ещё не проверен. Нажмите «Обновить»"
        : "Нет ответа на проверку статуса. Сервер может быть недоступен или скрывать статус настройкой приватности";
    const count = server.onlineCount == null ? "" : `\n${server.status === "available" ? "Подключено пользователей" : "Последнее известное число подключений"}: ${server.onlineCount}`;
    return `${label}${count}${server.lastAttemptAt ? `\nПроверка: ${new Date(server.lastAttemptAt).toLocaleString()}` : ""}${server.updatedAt ? `\nПолучено: ${new Date(server.updatedAt).toLocaleString()}` : ""}`;
}

function ServerPopulation({server}: {server: RecentServer}) {
    const count = server.onlineCount;
    const title = statusTitle(server);
    return <span className={`server-population ${server.status}`} title={title} role="img" aria-label={title}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" aria-hidden="true"><circle cx="12" cy="7" r="3"/><path d="M5 21v-3a7 7 0 0 1 14 0v3"/></svg>
        <span>{count ?? "—"}{server.status === "unavailable" && count != null ? "*" : ""}</span>
    </span>;
}

export function RecentServers({view, expanded, onToggle, onReconnect, onError, refreshingServers, onRefreshServers, standalone = false, disabled = false, onSelect, onCountChange}: {
    refreshingServers: boolean;
    onRefreshServers: () => void;
    onCountChange?: (count: number) => void;
    standalone?: boolean;
    disabled?: boolean;
    onSelect?: (address: string) => void;
    view: ClientViewDTO;
    expanded: boolean;
    onToggle: () => void;
    onReconnect: (address: string) => Promise<void>;
    onError: (message: string) => void;
}) {
    const [servers, setServers] = useState<RecentServer[]>([]);
    useEffect(() => { onCountChange?.(servers.length); }, [servers.length, onCountChange]);
    const [pending, setPending] = useState(false);
    const [editing, setEditing] = useState<string | null>(null);
    const [alias, setAlias] = useState("");
    const [menu, setMenu] = useState<{server: RecentServer; x: number; y: number} | null>(null);
    const menuRef = useRef<HTMLDivElement>(null);
    const editingRef = useRef<string | null>(null);
    const busy = useRef(false);
    const mounted = useRef(true);
    const loadGeneration = useRef(0);
    useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
    useEffect(() => {
        if (!menu) return;
        const outside = (event: PointerEvent) => { if (!menuRef.current?.contains(event.target as Node)) setMenu(null); };
        const escape = (event: KeyboardEvent) => { if (event.key === "Escape") setMenu(null); };
        const close = () => setMenu(null);
        document.addEventListener("pointerdown", outside);
        document.addEventListener("keydown", escape);
        window.addEventListener("resize", close);
        document.addEventListener("scroll", close, true);
        return () => {
            document.removeEventListener("pointerdown", outside);
            document.removeEventListener("keydown", escape);
            window.removeEventListener("resize", close);
            document.removeEventListener("scroll", close, true);
        };
    }, [menu]);
    useEffect(() => { setMenu(null); }, [expanded, view.chatContext, view.connectionStatus]);
    const load = useCallback(async () => {
        const generation = ++loadGeneration.current;
        try {
            const result = await desktopAPI.recentServers();
            if (mounted.current && generation === loadGeneration.current) setServers(result ?? []);
        } catch (error) { if (mounted.current && generation === loadGeneration.current) onError(String(error)); }
    }, [onError]);
    const visibleParticipantCount = expanded ? view.participants?.length : undefined;
    const visibleSnapshotFresh = expanded ? view.snapshotFresh : undefined;
    useEffect(() => { void load(); }, [load, view.chatContext, view.sessionId, view.connectionStatus, visibleParticipantCount, visibleSnapshotFresh, expanded]);
    useEffect(() => desktopAPI.onServerStatusChanged(() => { if (expanded) void load(); }), [load, expanded]);
    const run = async (operation: () => Promise<void>) => {
        if (busy.current) return;
        busy.current = true; setPending(true);
        try { await operation(); await load(); }
        catch (error) { onError(String(error)); }
        finally { busy.current = false; if (mounted.current) setPending(false); }
    };
    const reconnect = (server: RecentServer) => {
        if (disabled || server.current || (!standalone && view.connectionStatus !== "connected")) return;
        void run(() => onReconnect(server.address));
    };
    const rename = (server: RecentServer) => {
        if (busy.current) return;
        editingRef.current = server.address;
        setAlias(server.alias || ""); setEditing(server.address);
    };
    const saveAlias = () => {
        const address = editingRef.current;
        editingRef.current = null; setEditing(null);
        if (address) void run(() => desktopAPI.setServerAlias(address, alias));
    };
    return <section className={`sidebar-section recent-servers ${standalone ? "connection-server-list" : ""}`} aria-label="Последние серверы">
        <div className={`servers-heading ${standalone ? "panel-heading" : ""}`}>
        {standalone ? <h2>Серверы</h2> :
        <button className="servers-toggle" type="button" aria-expanded={expanded} aria-controls="recent-servers-list" onClick={onToggle}>
            <span className="servers-chevron" aria-hidden="true">{expanded ? "▾" : "▸"}</span><span>Серверы</span>
        </button>}
        <button type="button" className="servers-refresh" tabIndex={-1} disabled={refreshingServers || servers.length === 0}
                title={refreshingServers ? "Обновление статусов…" : "Обновить статусы сохранённых серверов"} aria-label="Обновить статусы сохранённых серверов" aria-busy={refreshingServers}
                onMouseDown={(event) => event.preventDefault()}
                onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") event.preventDefault(); }}
                onClick={(event) => { if (event.button === 0 && event.detail > 0) onRefreshServers(); }}>
            <Icon name="refresh"/>
        </button>
        <span className="count-badge">{servers.length}</span>
        </div>
        <div id="recent-servers-list" className="servers-scroll" hidden={!expanded}>
            {servers.map((server) => <div key={server.address} className="recent-server">
                {editing === server.address ? <input className="server-alias-input" autoFocus maxLength={64} value={alias}
                    aria-label="Название сервера" placeholder={server.address} onChange={(event) => setAlias(event.target.value)}
                    onBlur={saveAlias} onKeyDown={(event) => {
                        if (event.key === "Enter") { event.preventDefault(); event.currentTarget.blur(); }
                        if (event.key === "Escape") { editingRef.current = null; setEditing(null); }
                    }}/> :
                <button type="button" className="recent-server-connect" disabled={pending || disabled || (!standalone && view.connectionStatus !== "connected")}
                        title={`${server.address}\nДвойной клик — подключиться; правая кнопка мыши — действия; F2 — задать имя`}
                        aria-current={server.current ? "true" : undefined}
                        onClick={() => onSelect?.(server.address)}
                        onContextMenu={(event) => {
                            event.preventDefault();
                            setMenu({server, x: Math.max(8, Math.min(event.clientX, window.innerWidth - 180)), y: Math.max(8, Math.min(event.clientY, window.innerHeight - 90))});
                        }}
                        onDoubleClick={() => reconnect(server)}
                        onKeyDown={(event) => {
                            if (event.key === "F2") { event.preventDefault(); rename(server); }
                            if (event.key === "Delete") { event.preventDefault(); void run(() => desktopAPI.deleteRecentServer(server.address)); }
                            if (event.key === "Enter" && !event.repeat) { event.preventDefault(); reconnect(server); }
                        }}>
                    <span className={`server-availability ${server.status}`} role="img" title={statusTitle(server)} aria-label={statusTitle(server)}/>
                    <span className="recent-server-label"><span>{server.alias || server.address}</span>{server.alias && <small>{server.address}</small>}</span>
                </button>}
                <ServerPopulation server={server}/>
                <button type="button" className="server-favorite" aria-pressed={server.favorite}
                        aria-label={`${server.favorite ? "Открепить" : "Закрепить"} сервер ${server.alias || server.address}`}
                        title={server.favorite ? "Убрать из избранного" : "Закрепить в избранном"} disabled={pending}
                        onClick={() => void run(() => desktopAPI.setServerFavorite(server.address, !server.favorite))}>
                    <svg viewBox="0 0 24 24" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" aria-hidden="true"><path d="m12 3 2.8 5.7 6.3.9-4.6 4.5 1.1 6.3-5.6-3-5.6 3 1.1-6.3L2.9 9.6l6.3-.9Z"/></svg>
                </button>
            </div>)}
            {!servers.length && <p className="recent-servers-empty">Здесь появятся посещённые серверы.</p>}
        </div>
        {menu && <div ref={menuRef} className="server-actions-menu" role="group" aria-label="Действия с сервером" style={{left: menu.x, top: menu.y}}>
            <button type="button" autoFocus disabled={pending} onClick={() => { rename(menu.server); setMenu(null); }}>Задать имя</button>
            <button type="button" disabled={pending} onClick={() => {
                const address = menu.server.address;
                setMenu(null);
                void run(() => desktopAPI.deleteRecentServer(address));
            }}>Удалить из списка</button>
        </div>}
    </section>;
}
