import {useCallback, useEffect, useMemo, useRef, useState} from "react";
import type {AudioDeviceDTO, AudioDevicesDTO, ChannelDTO, ClientEventDTO, ClientViewDTO, ParticipantDTO} from "./api";
import {desktopAPI} from "./api";
import {buildChannelGroups, mergeEventTail} from "./model";

type Page = "channels" | "settings";
const serverAddressStorageKey = "govts.serverAddress";

function savedServerAddress(): string {
    try {
        return localStorage.getItem(serverAddressStorageKey) || "127.0.0.1:9000";
    } catch {
        return "127.0.0.1:9000";
    }
}

function rememberServerAddress(value: string) {
    try {
        localStorage.setItem(serverAddressStorageKey, value);
    } catch { /* The current session still keeps the controlled input value. */
    }
}

const emptyView: ClientViewDTO = {
    connectionStatus: "disconnected",
    server: {name: ""}, revision: "0", sessionId: "0", channelId: "0", snapshotFresh: false,
    channels: [], participants: [],
    audio: {
        muted: false,
        deafened: false,
        rnnoiseEnabled: true,
        rnnoiseSensitivity: 1,
        vadEnabled: false,
        vadMode: "hybrid",
        vadSensitivity: 0.5,
        vadOpen: false
    },
};

function errorText(error: unknown): string {
    return (error instanceof Error ? error.message : String(error)).replace(/^Error:\s*/, "");
}

function App() {
    const [view, setView] = useState<ClientViewDTO>(emptyView);
    const [page, setPage] = useState<Page>("channels");
    const [events, setEvents] = useState<ClientEventDTO[]>([]);
    const [actionError, setActionError] = useState("");
    const [eventPanelHeight, setEventPanelHeight] = useState(135);
    const lastSequence = useRef("0");

    const refresh = useCallback(async () => {
        try {
            const next = await desktopAPI.snapshot();
            next.channels ??= [];
            next.participants ??= [];
            setView(next);
        } catch (error) {
            setActionError(errorText(error));
        }
    }, []);

    const refreshEvents = useCallback(async () => {
        try {
            const next = (await desktopAPI.eventsAfter(lastSequence.current)) ?? [];
            if (!next.length) return;
            const newestSequence = next[next.length - 1].sequence;
            if (BigInt(newestSequence) > BigInt(lastSequence.current)) {
                lastSequence.current = newestSequence;
            }
            setEvents((current) => mergeEventTail(current, next));
        } catch (error) {
            setActionError(errorText(error));
        }
    }, []);

    const clearEvents = useCallback(() => {
        setEvents([]);
        lastSequence.current = "0";
    }, []);

    useEffect(() => {
        void refresh();
        void refreshEvents();
        const offState = desktopAPI.onStateChanged(() => void refresh());
        const offEvents = desktopAPI.onEventLogChanged(() => void refreshEvents());
        return () => {
            offState();
            offEvents();
        };
    }, [refresh, refreshEvents]);

    const invoke = useCallback(async (operation: () => Promise<unknown>) => {
        setActionError("");
        try {
            await operation();
            await refresh();
        } catch (error) {
            setActionError(errorText(error));
        }
    }, [refresh]);

    if (view.connectionStatus === "disconnected") {
        return (
            <ConnectionPage
                view={view}
                error={actionError}
                onError={setActionError}
                onRefresh={refresh}
                onClearEvents={clearEvents}
                onConnected={() => setPage("channels")}
            />
        );
    }

    return <div className="app-shell">
        <main className="main-area">
            <StatusBar view={view} page={page} onPageChange={setPage} invoke={invoke}/>
            {actionError && <div className="error-banner" role="alert">{actionError}</div>}
            {page === "channels"
                ? <ChannelsPage view={view} events={events} eventPanelHeight={eventPanelHeight}
                                onEventPanelHeightChange={setEventPanelHeight} invoke={invoke}/>
                : <SettingsPage view={view} invoke={invoke}/>}
        </main>
    </div>;
}

function ConnectionPage({view, error, onError, onRefresh, onClearEvents, onConnected}: {
    view: ClientViewDTO;
    error: string;
    onError: (value: string) => void;
    onRefresh: () => Promise<void>;
    onClearEvents: () => void;
    onConnected: () => void;
}) {
    const [server, setServer] = useState(savedServerAddress);
    const [name, setName] = useState("");
    const [pending, setPending] = useState(false);
    useEffect(() => {
        let active = true;
        void desktopAPI.savedDisplayName()
            .then((savedName) => {
                if (active) setName((current) => current || savedName);
            })
            .catch((loadError) => {
                if (active) onError(errorText(loadError));
            });
        return () => {
            active = false;
        };
    }, [onError]);
    const submit = async (event: React.FormEvent) => {
        event.preventDefault();
        if (pending) return;
        const address = server.trim();
        setServer(address);
        rememberServerAddress(address);
        onError("");
        setPending(true);
        try {
            onClearEvents();
            await desktopAPI.connect({name, server: address, initialChannel: "main"});
            await onRefresh();
            onConnected();
        } catch (connectError) {
            onError(errorText(connectError));
        } finally {
            setPending(false);
        }
    };
    return <main className="connection-page">
        <section className="connection-card">
            <div className="connection-logo">G</div>
            <p className="eyebrow">GOVTS DESKTOP</p><h1>Подключение к серверу</h1>
            <p className="lead">Введите адрес голосового сервера и имя, под которым вас увидят другие участники.</p>
            <form onSubmit={submit}>
                <label><span>Адрес сервера</span><input autoFocus value={server} onChange={(event) => {
                    setServer(event.target.value);
                    rememberServerAddress(event.target.value);
                }} placeholder="192.0.2.1:9000" spellCheck={false}/></label>
                <label><span>Отображаемое имя</span><input value={name}
                                                           onChange={(event) => setName(event.target.value)}
                                                           placeholder="Ваше имя" maxLength={64}/></label>
                {(error || view.lastError) && <div className="form-error" role="alert">{error || view.lastError}</div>}
                <button className="primary-button" disabled={pending || !server.trim() || !name.trim()}
                        type="submit">{pending ? "Подключаемся…" : "Подключиться"}</button>
            </form>
            <p></p>
        </section>
    </main>;
}

function StatusBar({view, page, onPageChange, invoke}: {
    view: ClientViewDTO;
    page: Page;
    onPageChange: (page: Page) => void;
    invoke: (operation: () => Promise<unknown>) => Promise<void>
}) {
    const labels: Record<string, string> = {
        connecting: "Подключение",
        connected: "Подключено",
        reconnecting: "Переподключение",
        disconnected: "Отключено"
    };
    return <header className="status-bar">
        <button className="header-nav-button"
                onClick={() => onPageChange(page === "settings" ? "channels" : "settings")}><span
            aria-hidden="true">{page === "settings" ? "←" : "⚙"}</span><span>{page === "settings" ? "К каналам" : "Настройки"}</span>
        </button>
        <div className="server-summary"><p className="eyebrow">СЕРВЕР</p><h1>{view.server.name || "Govts"}</h1></div>
        <div className="audio-control-island" role="group" aria-label="Управление звуком">
            <button className={`voice-control ${view.audio.muted ? "active" : ""}`} aria-pressed={view.audio.muted}
                    onClick={() => void invoke(() => desktopAPI.setMuted(!view.audio.muted))}>{view.audio.muted ? "Микрофон выкл." : "Микрофон"}</button>
            <button className={`voice-control ${view.audio.deafened ? "active" : ""}`}
                    aria-pressed={view.audio.deafened}
                    onClick={() => void invoke(() => desktopAPI.setDeafened(!view.audio.deafened))}>{view.audio.deafened ? "Звук выкл." : "Звук"}</button>
        </div>
        <div className={`status-pill ${view.connectionStatus}`}><span
            className="status-dot"/>{labels[view.connectionStatus] ?? view.connectionStatus}</div>
        {!view.snapshotFresh && <div className="sync-pill">Синхронизация…</div>}
        <button className="header-nav-button disconnect-button"
                onClick={() => void invoke(() => desktopAPI.disconnect())}><span
            aria-hidden="true">↪</span><span>Отключиться</span></button>
    </header>;
}

function ChannelsPage({view, events, eventPanelHeight, onEventPanelHeightChange, invoke}: {
    view: ClientViewDTO;
    events: ClientEventDTO[];
    eventPanelHeight: number;
    onEventPanelHeightChange: (height: number | ((current: number) => number)) => void;
    invoke: (operation: () => Promise<unknown>) => Promise<void>
}) {
    const channels = view.channels ?? [];
    const participants = view.participants ?? [];
    const [selectedID, setSelectedID] = useState(view.channelId !== "0" ? view.channelId : channels[0]?.id ?? "");
    useEffect(() => {
        if (selectedID && channels.some((channel) => channel.id === selectedID)) return;
        setSelectedID(view.channelId !== "0" ? view.channelId : channels[0]?.id ?? "");
    }, [channels, selectedID, view.channelId]);
    const selected = channels.find((channel) => channel.id === selectedID);
    return <section className="channels-page">
        <div className="channels-layout">
            <section className="panel channel-browser">
                <div className="panel-heading">
                    <div><p className="eyebrow">ПРОСТРАНСТВА</p><h2>Каналы</h2></div>
                    <span className="count-badge">{participants.length}</span></div>
                <div className="channel-scroll"><ChannelTree channels={channels} participants={participants}
                                                             selectedID={selectedID} currentID={view.channelId}
                                                             onSelect={setSelectedID} onJoin={(id) => {
                    if (id !== view.channelId && view.connectionStatus === "connected") void invoke(() => desktopAPI.joinChannel(id));
                }}/></div>
            </section>
            <section className="panel channel-detail">{selected ? <><p className="eyebrow">ВЫБРАННЫЙ КАНАЛ</p>
                <h2>{selected.name}</h2>
                <p className="channel-topic">{selected.topic || "Голосовой канал"}</p><p
                    className="channel-description">{selected.description || "Описание канала пока не задано."}</p>
                <div className="detail-grid">
                    <div>
                        <span>Участники</span><strong>{participants.filter((item) => item.channelId === selected.id).length}{selected.maxUsers ? ` / ${selected.maxUsers}` : ""}</strong>
                    </div>
                    <div><span>Кодек</span><strong>{selected.audio.codec.toUpperCase()}</strong></div>
                    <div><span>Частота</span><strong>{selected.audio.sampleRate / 1000} кГц</strong></div>
                    <div><span>Битрейт</span><strong>{selected.audio.bitrate / 1000} кбит/с</strong></div>
                </div>
            </> : <div className="empty-state">Выберите канал</div>}</section>
        </div>
        <EventPanel events={events} height={eventPanelHeight} onHeightChange={onEventPanelHeightChange}/>
    </section>;
}

function ChannelTree({channels, participants, selectedID, currentID, onSelect, onJoin}: {
    channels: ChannelDTO[];
    participants: ParticipantDTO[];
    selectedID: string;
    currentID: string;
    onSelect: (id: string) => void;
    onJoin: (id: string) => void
}) {
    const children = useMemo(() => {
        return buildChannelGroups(channels);
    }, [channels]);
    const renderLevel = (parentID: string, depth: number): React.ReactNode => (children.get(parentID) ?? []).map((channel) => {
        const members = participants.filter((participant) => participant.channelId === channel.id);
        return <div key={channel.id}>
            <button
                className={`channel-row ${selectedID === channel.id ? "selected" : ""} ${currentID === channel.id ? "current" : ""}`}
                style={{paddingLeft: 14 + depth * 18}} onClick={() => onSelect(channel.id)}
                onDoubleClick={() => onJoin(channel.id)}><span className="channel-icon" aria-hidden="true">⌁</span><span
                className="channel-name">{channel.name}</span><span className="channel-count">{members.length}</span>
            </button>
            {members.map((participant) => <ParticipantRow key={participant.sessionId} participant={participant}
                                                          depth={depth}/>)}{renderLevel(channel.id, depth + 1)}</div>;
    });
    return <div className="channel-tree">{channels.length ? renderLevel("0", 0) :
        <div className="empty-state">Каналы ещё не загружены</div>}</div>;
}

function ParticipantRow({participant, depth}: { participant: ParticipantDTO; depth: number }) {
    return <div className={`participant-row ${participant.speaking ? "speaking" : ""}`}
                style={{paddingLeft: 46 + depth * 18}}><span
        className="avatar">{participant.displayName.slice(0, 1).toUpperCase()}</span><span>{participant.displayName}{participant.local ? " (вы)" : ""}</span><span
        className="speaking-ring" aria-label={participant.speaking ? "Говорит" : "Не говорит"}/></div>;
}

function EventPanel({events, height, onHeightChange}: {
    events: ClientEventDTO[];
    height: number;
    onHeightChange: (height: number | ((current: number) => number)) => void
}) {
    const listRef = useRef<HTMLDivElement>(null);
    const dockRef = useRef<HTMLDivElement>(null);
    const stickToBottom = useRef(true);
    const dragRef = useRef<{ startY: number; startHeight: number; maxHeight: number } | null>(null);
    useEffect(() => {
        if (stickToBottom.current && listRef.current) listRef.current.scrollTop = listRef.current.scrollHeight;
    }, [events]);
    useEffect(() => {
        const move = (event: PointerEvent) => {
            const drag = dragRef.current;
            if (!drag) return;
            onHeightChange(Math.max(135, Math.min(drag.maxHeight, drag.startHeight + drag.startY - event.clientY)));
        };
        const stop = () => {
            if (!dragRef.current) return;
            dragRef.current = null;
            document.body.style.removeProperty("cursor");
            document.body.style.removeProperty("user-select");
        };
        window.addEventListener("pointermove", move);
        window.addEventListener("pointerup", stop);
        window.addEventListener("pointercancel", stop);
        return () => {
            stop();
            window.removeEventListener("pointermove", move);
            window.removeEventListener("pointerup", stop);
            window.removeEventListener("pointercancel", stop);
        };
    }, [onHeightChange]);
    useEffect(() => {
        const page = dockRef.current?.parentElement;
        if (!page) return;
        const observer = new ResizeObserver(() => onHeightChange((current) => Math.min(current, Math.max(135, page.clientHeight - 250))));
        observer.observe(page);
        return () => observer.disconnect();
    }, [onHeightChange]);
    const startResize = (event: React.PointerEvent<HTMLDivElement>) => {
        if (event.button !== 0) return;
        const page = dockRef.current?.parentElement;
        if (!page) return;
        event.preventDefault();
        dragRef.current = {
            startY: event.clientY,
            startHeight: height,
            maxHeight: Math.max(135, page.clientHeight - 250)
        };
        document.body.style.cursor = "ns-resize";
        document.body.style.userSelect = "none";
    };
    return <div className="event-dock" ref={dockRef} style={{height}}>
        <div className="event-resize-handle" role="separator" aria-label="Изменить высоту журнала событий"
             aria-orientation="horizontal" onPointerDown={startResize}></div>
        <section className="panel event-panel">
            <div className="event-list" ref={listRef} onScroll={(event) => {
                const element = event.currentTarget;
                stickToBottom.current = element.scrollHeight - element.scrollTop - element.clientHeight < 12;
            }}>{events.length === 0 ?
                <div className="empty-event">События появятся после подключения</div> : events.map((item) => <div
                    className={`event-row kind-${item.kind}`} key={item.sequence}>
                    <time>{new Date(item.time).toLocaleTimeString("ru-RU", {
                        hour: "2-digit",
                        minute: "2-digit",
                        second: "2-digit"
                    })}</time>
                    <span className="event-marker"/><span>{item.message}</span></div>)}</div>
        </section>
    </div>;
}

function SettingsPage({view, invoke}: {
    view: ClientViewDTO;
    invoke: (operation: () => Promise<unknown>) => Promise<void>
}) {
    const [sensitivity, setSensitivity] = useState(view.audio.vadSensitivity);
    useEffect(() => setSensitivity(view.audio.vadSensitivity), [view.audio.vadSensitivity]);
    const [rnnoiseSensitivity, setRNNoiseSensitivity] = useState(view.audio.rnnoiseSensitivity);
    useEffect(() => setRNNoiseSensitivity(view.audio.rnnoiseSensitivity), [view.audio.rnnoiseSensitivity]);
    const [devices, setDevices] = useState<AudioDevicesDTO | null>(null);
    const [devicesError, setDevicesError] = useState("");
    const [devicePending, setDevicePending] = useState(false);
    const loadDevices = useCallback(async () => {
        try {
            const next = await desktopAPI.audioDevices();
            next.capture ??= [];
            next.playback ??= [];
            setDevices(next);
            setDevicesError("");
        } catch (error) {
            setDevicesError(errorText(error));
        }
    }, []);
    useEffect(() => {
        void loadDevices();
    }, [loadDevices]);
    const selectDevice = async (kind: "capture" | "playback", id: string) => {
        if (devicePending) return;
        setDevicePending(true);
        setDevicesError("");
        try {
            if (kind === "capture") await desktopAPI.setCaptureDevice(id);
            else await desktopAPI.setPlaybackDevice(id);
            await loadDevices();
        } catch (error) {
            setDevicesError(errorText(error));
        } finally {
            setDevicePending(false);
        }
    };
    return <section className="settings-page">
        <div className="settings-heading"><p className="eyebrow">ПАРАМЕТРЫ КЛИЕНТА</p><h2>Настройки</h2></div>
        {devicesError && <div className="device-error" role="alert">Не удалось получить аудиоустройства: {devicesError}
            <button className="text-button" onClick={() => void loadDevices()}>Повторить</button>
        </div>}
        <section className="settings-card"><h3>Микрофон</h3><DeviceSelect id="capture-device"
                                                                          label="Устройство захвата звука"
                                                                          devices={devices?.capture ?? []}
                                                                          value={devices?.selectedCapture ?? ""}
                                                                          disabled={!devices || devicePending}
                                                                          onChange={(id) => selectDevice("capture", id)}/>
            <SettingToggle title="Шумоподавление"
                           description="Убирает постоянный фоновый шум до анализа голосовой активности."
                           checked={view.audio.rnnoiseEnabled}
                           onChange={(value) => invoke(() => desktopAPI.setRNNoiseEnabled(value))}/>
            <SensitivitySlider label="Интенсивность шумоподавления"
                               description="Чем выше значение, тем сильнее подавляется фоновый шум."
                               value={rnnoiseSensitivity} disabled={!view.audio.rnnoiseEnabled}
                               onChange={setRNNoiseSensitivity}
                               onCommit={(value) => invoke(() => desktopAPI.setRNNoiseSensitivity(value))}/>
            <SettingToggle title="Обнаружение голоса (VAD)"
                           description="Микрофон передаёт звук, когда обнаружена речь."
                           checked={view.audio.vadEnabled}
                           onChange={(value) => invoke(() => desktopAPI.setVADEnabled(value))}/>
            <ModeSelect value={view.audio.vadMode} disabled={!view.audio.vadEnabled}
                        open={view.audio.vadOpen}
                        onChange={(value) => invoke(() => desktopAPI.setVADMode(value))}/>
            <div className="sensitivity-setting">
                <div><span className="setting-label">Порог передачи звука</span>
                    <output>{Math.round((1 - sensitivity) * 100)}%</output>
                </div>
                <p>Чем выше значение, тем тише может быть речь, открывающая микрофон.</p></div>
            <AudioWaveform sensitivity={sensitivity} disabled={!view.audio.vadEnabled}
                           onSensitivityChange={setSensitivity}
                           onSensitivityCommit={(value) => invoke(() => desktopAPI.setVADSensitivity(value))}/>
        </section>
        <section className="settings-card compact-card"><h3>Воспроизведение</h3><DeviceSelect id="playback-device"
                                                                                              label="Устройство вывода звука"
                                                                                              devices={devices?.playback ?? []}
                                                                                              value={devices?.selectedPlayback ?? ""}
                                                                                              disabled={!devices || devicePending}
                                                                                              onChange={(id) => selectDevice("playback", id)}/><SettingToggle
            title="Заглушить звук" description="Входящий голос продолжает обрабатываться, но не воспроизводится."
            checked={view.audio.deafened} onChange={(value) => invoke(() => desktopAPI.setDeafened(value))}/></section>
    </section>;
}

function SensitivitySlider({label, description, value, disabled, onChange, onCommit}: {
    label: string;
    description: string;
    value: number;
    disabled: boolean;
    onChange: (value: number) => void;
    onCommit: (value: number) => Promise<void>;
}) {
    const commit = (value: string) => void onCommit(Number(value));
    return <div className={`sensitivity-setting slider-setting ${disabled ? "disabled" : ""}`}>
        <div><label className="setting-label" htmlFor="rnnoise-sensitivity">{label}</label>
            <output htmlFor="rnnoise-sensitivity">{Math.round(value * 100)}%</output>
        </div>
        <p>{description}</p>
        <input id="rnnoise-sensitivity" aria-label={label} type="range" min="0" max="1" step="0.01"
               value={value} disabled={disabled}
               onChange={(event) => onChange(Number(event.target.value))}
               onPointerUp={(event) => commit(event.currentTarget.value)}
               onKeyUp={(event) => commit(event.currentTarget.value)}/>
    </div>;
}

function AudioWaveform({sensitivity, disabled, onSensitivityChange, onSensitivityCommit}: {
    sensitivity: number;
    disabled: boolean;
    onSensitivityChange: (value: number) => void;
    onSensitivityCommit: (value: number) => Promise<void>
}) {
    const canvasRef = useRef<HTMLCanvasElement>(null);
    const thresholdRef = useRef<HTMLDivElement>(null);
    const targetRef = useRef({input: 0, processed: 0, transmitted: 0, updatedAt: 0});
    const thresholdPosition = 1 - sensitivity;
    const thresholdLevel = (40 - 35 * sensitivity) / 60;
    const thresholdLevelRef = useRef(thresholdLevel);
    const sensitivityFromPosition = (position: number) => 1 - position;

    useEffect(() => {
        thresholdLevelRef.current = thresholdLevel;
        thresholdRef.current?.style.setProperty("--threshold", `${thresholdPosition * 100}%`);
    }, [thresholdLevel, thresholdPosition]);

    useEffect(() => desktopAPI.onAudioMeter((sample) => {
        targetRef.current = {
            input: Math.max(0, Math.min(1, sample.input)),
            processed: Math.max(0, Math.min(1, sample.processed ?? sample.input)),
            transmitted: Math.max(0, Math.min(1, sample.transmitted)),
            updatedAt: performance.now(),
        };
    }), []);

    useEffect(() => {
        const canvas = canvasRef.current;
        if (!canvas) return;
        const context = canvas.getContext("2d");
        if (!context) return;
        const points = 96;
        const rejectedHistory = Array<number>(points).fill(0);
        const transmittedHistory = Array<number>(points).fill(0);
        let currentInput = 0;
        let currentProcessed = 0;
        let currentTransmitted = 0;
        let previousSample = performance.now();
        let animationFrame = 0;

        const resize = () => {
            const bounds = canvas.getBoundingClientRect();
            const scale = window.devicePixelRatio || 1;
            canvas.width = Math.max(1, Math.round(bounds.width * scale));
            canvas.height = Math.max(1, Math.round(bounds.height * scale));
            context.setTransform(scale, 0, 0, scale, 0, 0);
        };
        const observer = new ResizeObserver(resize);
        observer.observe(canvas);
        resize();

        const ribbon = (history: number[], center: number, height: number, fill: string, stroke: string) => {
            const width = canvas.clientWidth;
            const step = width / Math.max(1, history.length - 1);
            context.beginPath();
            history.forEach((value, index) => {
                const y = center - Math.max(0.6, value * height);
                if (index === 0) context.moveTo(0, y);
                else context.lineTo(index * step, y);
            });
            for (let index = history.length - 1; index >= 0; index--) {
                context.lineTo(index * step, center + Math.max(0.6, history[index] * height));
            }
            context.closePath();
            context.fillStyle = fill;
            context.fill();
            context.strokeStyle = stroke;
            context.lineWidth = 1;
            context.stroke();
        };

        const draw = (now: number) => {
            const target = targetRef.current;
            const stale = now - target.updatedAt > 120;
            const inputTarget = stale ? 0 : target.input;
            const processedTarget = stale ? 0 : target.processed;
            const transmittedTarget = stale ? 0 : target.transmitted;
            currentInput += (inputTarget - currentInput) * (inputTarget > currentInput ? 0.42 : 0.16);
            currentProcessed += (processedTarget - currentProcessed) * (processedTarget > currentProcessed ? 0.45 : 0.035);
            currentTransmitted += (transmittedTarget - currentTransmitted) * (transmittedTarget > currentTransmitted ? 0.48 : 0.2);
            if (thresholdRef.current) {
                const levelOnThresholdScale = Math.max(0, Math.min(1, (currentProcessed - 1 / 12) / (7 / 12)));
                thresholdRef.current.style.setProperty("--level", `${levelOnThresholdScale * 100}%`);
                thresholdRef.current.classList.toggle("open", processedTarget >= thresholdLevelRef.current && processedTarget > 0.003);
            }
            if (now - previousSample >= 25) {
                const rejected = Math.max(0, currentInput - currentTransmitted);
                rejectedHistory.shift();
                rejectedHistory.push(rejected);
                transmittedHistory.shift();
                transmittedHistory.push(currentTransmitted);
                previousSample = now;
            }
            const width = canvas.clientWidth;
            const height = canvas.clientHeight;
            context.clearRect(0, 0, width, height);
            const waveCenter = height * 0.5;
            context.beginPath();
            context.moveTo(0, waveCenter);
            context.lineTo(width, waveCenter);
            context.strokeStyle = "rgba(126, 143, 174, .10)";
            context.lineWidth = 1;
            context.stroke();
            ribbon(rejectedHistory, waveCenter, height * 0.38, "rgba(126, 143, 174, .18)", "rgba(151, 166, 193, .34)");
            ribbon(transmittedHistory, waveCenter, height * 0.38, "rgba(19, 231, 176, .24)", "rgba(28, 240, 184, .9)");
            animationFrame = requestAnimationFrame(draw);
        };
        animationFrame = requestAnimationFrame(draw);
        return () => {
            cancelAnimationFrame(animationFrame);
            observer.disconnect();
        };
    }, []);

    const updateThreshold = (position: number, commit: boolean) => {
        const value = sensitivityFromPosition(position);
        onSensitivityChange(value);
        if (commit) void onSensitivityCommit(value);
    };
    return <>
        <div className={`threshold-meter ${disabled ? "disabled" : ""}`} ref={thresholdRef}><span
            className="threshold-track"><span className="threshold-level"/></span><span className="threshold-marker"
                                                                                        aria-hidden="true"/><input
            aria-label="Порог голосовой активности" type="range" min="0" max="1" step="0.01" value={thresholdPosition}
            disabled={disabled} onChange={(event) => updateThreshold(Number(event.target.value), false)}
            onPointerUp={(event) => updateThreshold(Number(event.currentTarget.value), true)}
            onKeyUp={(event) => updateThreshold(Number(event.currentTarget.value), true)}/></div>
        <div className="audio-waveform">
            <div className="audio-waveform-heading"><span className="setting-label">Активность микрофона</span><span
                className="wave-legend"><i className="transmitted"/>Передаётся<i className="rejected"/>Отсечено</span>
            </div>
            <canvas ref={canvasRef} role="img" aria-label="Индикатор передаваемого и отсечённого звука"/>
        </div>
    </>;
}

function DeviceSelect({id, label, devices, value, disabled, onChange}: {
    id: string;
    label: string;
    devices: AudioDeviceDTO[];
    value: string;
    disabled: boolean;
    onChange: (id: string) => Promise<void>
}) {
    const [open, setOpen] = useState(false);
    const selected = devices.find((device) => device.id === value);
    const selectedName = selected ? `${selected.name}${selected.isDefault ? " — системное по умолчанию" : ""}` : "Системное устройство по умолчанию";
    useEffect(() => {
        if (!open) return;
        const closeOnEscape = (event: KeyboardEvent) => {
            if (event.key === "Escape") setOpen(false);
        };
        window.addEventListener("keydown", closeOnEscape);
        return () => {
            window.removeEventListener("keydown", closeOnEscape);
        };
    }, [open]);
    const choose = (deviceID: string) => {
        setOpen(false);
        if (deviceID !== value) void onChange(deviceID);
    };
    return <div className="device-select"><span className="setting-label" id={`${id}-label`}>{label}</span>
        <div className="device-select-control">
            <button id={id} className="device-select-trigger" type="button" disabled={disabled}
                    aria-labelledby={`${id}-label ${id}`} aria-haspopup="listbox" aria-expanded={open}
                    onClick={() => setOpen((current) => !current)}><span>{selectedName}</span></button>
            {open && <div className="device-options" role="listbox" aria-labelledby={`${id}-label`}>
                <button type="button" role="option" aria-selected={value === ""}
                        className={value === "" ? "selected" : ""} onClick={() => choose("")}>Системное устройство по
                    умолчанию
                </button>
                {devices.map((device) => <button type="button" role="option" aria-selected={device.id === value}
                                                 className={device.id === value ? "selected" : ""}
                                                 onClick={() => choose(device.id)}
                                                 key={device.id}>{device.name}{device.isDefault ?
                    <small>Системное по умолчанию</small> : null}</button>)}</div>}</div>
    </div>;
}

function ModeSelect({value, disabled, open, onChange}: {
    value: string;
    disabled: boolean;
    open: boolean;
    onChange: (value: string) => Promise<void>;
}) {
    const [optionsOpen, setOptionsOpen] = useState(false);
    const options = [
        {value: "level", label: "По громкости"},
        {value: "vad", label: "Распознавание речи"},
        {value: "hybrid", label: "Гибридный"},
    ];
    const selectedName = options.find((option) => option.value === value)?.label ?? value;
    useEffect(() => {
        if (!optionsOpen) return;
        const closeOnEscape = (event: KeyboardEvent) => {
            if (event.key === "Escape") setOptionsOpen(false);
        };
        window.addEventListener("keydown", closeOnEscape);
        return () => window.removeEventListener("keydown", closeOnEscape);
    }, [optionsOpen]);
    const choose = (nextValue: string) => {
        setOptionsOpen(false);
        if (nextValue !== value) void onChange(nextValue);
    };
    return <div className="device-select mode-select">
        <div className="mode-select-heading"><span className="setting-label" id="vad-mode-label">Режим</span><span
            className={`gate-indicator ${open ? "open" : ""}`}>{open ? "Передача" : "Ожидание речи"}</span></div>
        <div className="device-select-control">
            <button id="vad-mode" className="device-select-trigger" type="button" disabled={disabled}
                    aria-labelledby="vad-mode-label vad-mode" aria-haspopup="listbox" aria-expanded={optionsOpen}
                    onClick={() => setOptionsOpen((current) => !current)}><span>{selectedName}</span></button>
            {optionsOpen && <div className="device-options" role="listbox" aria-labelledby="vad-mode-label">
                {options.map((option) => <button type="button" role="option" aria-selected={option.value === value}
                                                 className={option.value === value ? "selected" : ""}
                                                 onClick={() => choose(option.value)}
                                                 key={option.value}>{option.label}</button>)}
            </div>}
        </div>
    </div>;
}

function SettingToggle({title, description, checked, onChange}: {
    title: string;
    description: string;
    checked: boolean;
    onChange: (value: boolean) => Promise<void>
}) {
    return <div className="setting-toggle">
        <div><strong>{title}</strong><p>{description}</p></div>
        <label className="switch"><input type="checkbox" checked={checked}
                                         onChange={(event) => void onChange(event.target.checked)}/><span/></label>
    </div>;
}

export default App;
