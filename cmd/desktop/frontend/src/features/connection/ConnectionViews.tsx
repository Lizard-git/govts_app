import {useEffect, useRef, useState} from "react";
import type {ReactNode} from "react";
import type {ClientViewDTO} from "../../api";
import {desktopAPI} from "../../api";
import {ConnectionStatsPopup} from "./ConnectionStatsPopup";
import {RecentServers} from "./RecentServers";
import {Icon} from "../../components/Icon";
import {errorCode, errorText, rawError} from "../../locales";
import {LocaleSwitch, useLocale} from "../../locales/Locale";
import type {ScreenSharingState} from "../screen/ScreenShareDialog";
import type {ScreenMediaController} from "../screen/screenMedia";

export type Page = "channels" | "settings";
const serverAddressStorageKey = "sonoryx.serverAddress";

function savedServerAddress(): string {
    try { return localStorage.getItem(serverAddressStorageKey) || "127.0.0.1:9000"; }
    catch { return "127.0.0.1:9000"; }
}

export function rememberServerAddress(value: string) {
    try { localStorage.setItem(serverAddressStorageKey, value); }
    catch { /* Keep the controlled input value for this session. */ }
}

export function ConnectionPage({view, error, onError, onRefresh, onClearEvents, onConnected, refreshingServers, onRefreshServers}: {
    refreshingServers: boolean;
    onRefreshServers: () => void;
    view: ClientViewDTO;
    error: string;
    onError: (value: string) => void;
    onRefresh: () => Promise<void>;
    onClearEvents: () => void;
    onConnected: () => void;
}) {
    const {m} = useLocale();
    const [server, setServer] = useState(savedServerAddress);
    const [name, setName] = useState("");
    const [pending, setPending] = useState(false);
    const [serverCount, setServerCount] = useState(0);
    const connecting = useRef(false);
    useEffect(() => {
        let active = true;
        void desktopAPI.savedDisplayName()
            .then((savedName) => { if (active) setName((current) => current || savedName); })
            .catch((loadError) => { if (active) onError(rawError(loadError)); });
        return () => { active = false; };
    }, [onError]);
    const connect = async (value: string) => {
        if (connecting.current) return;
        const address = value.trim();
        setServer(address);
        rememberServerAddress(address);
        onError("");
        if (!name.trim()) { onError(errorCode("name-required")); return; }
        connecting.current = true;
        setPending(true);
        try {
            onClearEvents();
            await desktopAPI.connect({name, server: address});
            await onRefresh();
            onConnected();
        } catch (connectError) {
            onError(rawError(connectError));
        } finally {
            connecting.current = false;
            setPending(false);
        }
    };
    const selectServer = (address: string) => { setServer(address); rememberServerAddress(address); };
    return <div className="connection-page"><div className={`connection-layout ${serverCount === 0 ? "connection-layout-empty" : ""}`}>
        <section className="connection-card connection-server-card" hidden={serverCount === 0}>
            <RecentServers view={view} expanded={true} onToggle={() => {}} standalone disabled={pending}
                refreshingServers={refreshingServers} onRefreshServers={onRefreshServers}
                onSelect={selectServer} onReconnect={connect} onError={onError} onCountChange={setServerCount}/>
        </section>
        <section className="connection-card">
        <div className="connection-logo">SRX</div>
        <h1>{m.connection.title}</h1>
        <p className="lead">{m.connection.lead}</p>
        <form onSubmit={(event) => { event.preventDefault(); void connect(server); }}>
            <label><span>{m.connection.address}</span><input autoFocus value={server} onChange={(event) => {
                setServer(event.target.value); rememberServerAddress(event.target.value);
            }} placeholder={m.connection.addressPlaceholder} spellCheck={false}/></label>
            <label><span>{m.connection.name}</span><input id="display-name" value={name} onChange={(event) => setName(event.target.value)}
                placeholder={m.connection.namePlaceholder} maxLength={64}/></label>
            {(error || view.lastError) && <div className="form-error" role="alert">{errorText(error || view.lastError, m)}</div>}
            <button className="primary-button" disabled={pending || !server.trim() || !name.trim()}
                type="submit">{pending ? m.connection.connecting : m.connection.connect}</button>
        </form><p></p>
    </section></div></div>;
}

// LobbyBar is the header shown before joining a server: client version,
// update notice and settings, without any session controls.
export function LobbyBar({version, updateAction, onPageChange}: {
    version?: string;
    updateAction?: ReactNode;
    onPageChange: (page: Page) => void;
}) {
    const {m} = useLocale();
    return <header className="status-bar">
        <div className="app-brand"><span className="brand-mark"><Icon name="server"/></span><strong>Sonoryx</strong>{version && <span className="server-version" aria-label={m.connection.clientVersion(version)}>{version}</span>}{updateAction}</div>
        <LocaleSwitch variant="header"/>
        <button id="open-settings" className="status-pill settings-pill" type="button" onClick={() => onPageChange("settings")}>
            <Icon name="settings"/><span>{m.connection.settings}</span>
        </button>
    </header>;
}

export function StatusBar({view, onPageChange, sharing, screenMedia, updateAction, invoke}: {
    invoke: (operation: () => Promise<unknown>) => Promise<void>;
    updateAction?: ReactNode;
    view: ClientViewDTO;
    onPageChange: (page: Page) => void;
    sharing: ScreenSharingState;
    screenMedia: ScreenMediaController;
}) {
    const {m} = useLocale();
    return <header className="status-bar">
        <div className="app-brand"><span className="brand-mark"><Icon name="server"/></span><strong title={view.server.name}>{view.server.name || m.connection.server}</strong>{view.server.version && <span className="server-version" aria-label={m.connection.serverVersion(view.server.version)}>{view.server.version}</span>}{updateAction}</div>
        <div className={`sync-pill ${view.snapshotFresh ? "synced" : ""}`} aria-hidden={view.snapshotFresh}>{m.connection.syncing}</div>
        <ConnectionStatsPopup sessionId={view.sessionId} status={view.connectionStatus} channelId={view.channelId} sharing={sharing} screenMedia={screenMedia}/>
        <button id="open-settings" className="status-pill settings-pill" type="button" onClick={() => onPageChange("settings")}>
            <Icon name="settings"/><span>{m.connection.settings}</span>
        </button>
        <button className="status-pill settings-pill disconnect-button" type="button" title={m.connection.disconnect}
            onClick={() => void invoke(() => desktopAPI.disconnect())}><Icon name="hangup"/><span>{m.connection.leave}</span></button>
    </header>;
}

export function AudioControls({view, invoke, sharing}: {
    view: ClientViewDTO;
    invoke: (operation: () => Promise<unknown>) => Promise<void>;
    sharing: ScreenSharingState;
}) {
    const {m} = useLocale();
    return <div className="audio-control-island" role="group" aria-label={m.connection.audioGroup}>
            <button className={`voice-control ${view.audio.muted || !view.audio.captureAvailable ? "active" : ""}`}
                aria-pressed={view.audio.muted} disabled={!view.audio.captureAvailable}
                title={!view.audio.captureAvailable ? m.connection.noMic : view.audio.muted ? m.connection.micOn : m.connection.micOff}
                onClick={() => void invoke(() => desktopAPI.setMuted(!view.audio.muted))}><Icon name="mic"/>{m.connection.mic}</button>
            <button className={`voice-control ${view.audio.deafened ? "active" : ""}`} aria-pressed={view.audio.deafened}
                title={view.audio.deafened ? m.connection.soundOn : m.connection.soundOff}
                onClick={() => void invoke(() => desktopAPI.setDeafened(!view.audio.deafened))}><Icon name="sound"/>{m.connection.audio}</button>
        <button ref={sharing.buttonRef} className={`voice-control screen-share-button ${sharing.active ? "sharing" : ""}`}
            title={sharing.active ? m.connection.stopShare : m.connection.startShare} aria-pressed={sharing.active}
            aria-haspopup={sharing.active ? undefined : "dialog"} disabled={sharing.pending || !sharing.available}
            onClick={() => void sharing.toggle()}><Icon name="screen"/>{sharing.active ? m.connection.stopShare : m.connection.sharing}</button>
    </div>;
}
