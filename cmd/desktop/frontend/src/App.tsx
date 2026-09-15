import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import { Service } from "../bindings/example.com/go-voice-mvp/internal/ui/wails";
import type { AudioDeviceDTO, AudioDevicesDTO, ChannelDTO, ClientEventDTO, ClientViewDTO, ParticipantDTO } from "../bindings/example.com/go-voice-mvp/internal/ui/wails";
import { buildChannelGroups, mergeEventTail } from "./model";

type Page = "channels" | "settings";

const emptyView: ClientViewDTO = {
  connectionStatus: "disconnected",
  server: { name: "" }, revision: "0", sessionId: "0", channelId: "0", snapshotFresh: false,
  channels: [], participants: [],
  audio: { muted: false, deafened: false, rnnoiseEnabled: true, vadEnabled: false, vadMode: "hybrid", vadSensitivity: 0.5, vadOpen: false },
};

function errorText(error: unknown): string {
  return (error instanceof Error ? error.message : String(error)).replace(/^Error:\s*/, "");
}

function App() {
  const [view, setView] = useState<ClientViewDTO>(emptyView);
  const [page, setPage] = useState<Page>("channels");
  const [events, setEvents] = useState<ClientEventDTO[]>([]);
  const [actionError, setActionError] = useState("");
  const lastSequence = useRef("0");

  const refresh = useCallback(async () => {
    try {
      const next = await Service.Snapshot();
      next.channels ??= [];
      next.participants ??= [];
      setView(next);
    } catch (error) { setActionError(errorText(error)); }
  }, []);

  const refreshEvents = useCallback(async () => {
    try {
      const next = (await Service.EventsAfter(lastSequence.current)) ?? [];
      if (!next.length) return;
      const newestSequence = next[next.length - 1].sequence;
      if (BigInt(newestSequence) > BigInt(lastSequence.current)) {
        lastSequence.current = newestSequence;
      }
      setEvents((current) => mergeEventTail(current, next));
    } catch (error) { setActionError(errorText(error)); }
  }, []);

  useEffect(() => {
    void refresh();
    void refreshEvents();
    const offState = Events.On("client-state-changed", () => void refresh());
    const offEvents = Events.On("client-event-log-changed", () => void refreshEvents());
    return () => { offState(); offEvents(); };
  }, [refresh, refreshEvents]);

  const invoke = useCallback(async (operation: () => Promise<unknown>) => {
    setActionError("");
    try { await operation(); await refresh(); } catch (error) { setActionError(errorText(error)); }
  }, [refresh]);

  if (view.connectionStatus === "disconnected") {
    return <ConnectionPage view={view} error={actionError} onError={setActionError} onRefresh={refresh} />;
  }

  return <div className="app-shell">
    <aside className="sidebar">
      <div className="brand-mark" aria-label="Govots">G</div>
      <nav aria-label="Основная навигация">
        <button className={page === "channels" ? "nav-button active" : "nav-button"} onClick={() => setPage("channels")}><span aria-hidden="true">◫</span><span>Каналы</span></button>
      </nav>
      <div className="sidebar-spacer" />
      <nav className="sidebar-secondary" aria-label="Настройки клиента">
        <button className={page === "settings" ? "nav-button active" : "nav-button"} onClick={() => setPage("settings")}><span aria-hidden="true">⚙</span><span>Настройки</span></button>
      </nav>
      <button className="disconnect-button" onClick={() => void invoke(() => Service.Disconnect())}>Отключиться</button>
    </aside>
    <main className="main-area">
      <StatusBar view={view} />
      {actionError && <div className="error-banner" role="alert">{actionError}</div>}
      {page === "channels"
        ? <ChannelsPage view={view} events={events} onClearEvents={() => setEvents([])} invoke={invoke} />
        : <SettingsPage view={view} invoke={invoke} />}
      <VoiceBar view={view} invoke={invoke} />
    </main>
  </div>;
}

function ConnectionPage({ view, error, onError, onRefresh }: { view: ClientViewDTO; error: string; onError: (value: string) => void; onRefresh: () => Promise<void> }) {
  const [server, setServer] = useState("127.0.0.1:9000");
  const [name, setName] = useState("");
  const [pending, setPending] = useState(false);
  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    if (pending) return;
    onError(""); setPending(true);
    try { await Service.Connect({ name, server, initialChannel: "main" }); await onRefresh(); }
    catch (connectError) { onError(errorText(connectError)); }
    finally { setPending(false); }
  };
  return <main className="connection-page"><section className="connection-card">
    <div className="connection-logo">G</div><p className="eyebrow">GOVOTS DESKTOP</p><h1>Подключение к серверу</h1>
    <p className="lead">Введите адрес голосового сервера и имя, под которым вас увидят другие участники.</p>
    <form onSubmit={submit}>
      <label><span>Адрес сервера</span><input autoFocus value={server} onChange={(event) => setServer(event.target.value)} placeholder="192.0.2.1:9000" spellCheck={false} /></label>
      <label><span>Отображаемое имя</span><input value={name} onChange={(event) => setName(event.target.value)} placeholder="Ваше имя" maxLength={64} /></label>
      {(error || view.lastError) && <div className="form-error" role="alert">{error || view.lastError}</div>}
      <button className="primary-button" disabled={pending || !server.trim() || !name.trim()} type="submit">{pending ? "Подключаемся…" : "Подключиться"}</button>
    </form><p className="connection-hint">Можно указать IP без порта — будет использован порт 9000.</p>
  </section></main>;
}

function StatusBar({ view }: { view: ClientViewDTO }) {
  const labels: Record<string, string> = { connecting: "Подключение", connected: "Подключено", reconnecting: "Переподключение", disconnected: "Отключено" };
  return <header className="status-bar"><div><p className="eyebrow">СЕРВЕР</p><h1>{view.server.name || "Govots"}</h1></div>
    <div className={`status-pill ${view.connectionStatus}`}><span className="status-dot" />{labels[view.connectionStatus] ?? view.connectionStatus}</div>
    {!view.snapshotFresh && <div className="sync-pill">Синхронизация…</div>}
  </header>;
}

function ChannelsPage({ view, events, onClearEvents, invoke }: { view: ClientViewDTO; events: ClientEventDTO[]; onClearEvents: () => void; invoke: (operation: () => Promise<unknown>) => Promise<void> }) {
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
      <section className="panel channel-browser"><div className="panel-heading"><div><p className="eyebrow">ПРОСТРАНСТВА</p><h2>Каналы</h2></div><span className="count-badge">{participants.length}</span></div>
        <div className="channel-scroll"><ChannelTree channels={channels} participants={participants} selectedID={selectedID} currentID={view.channelId} onSelect={setSelectedID} /></div>
      </section>
      <section className="panel channel-detail">{selected ? <><p className="eyebrow">ВЫБРАННЫЙ КАНАЛ</p><h2>{selected.name}</h2>
        <p className="channel-topic">{selected.topic || "Голосовой канал"}</p><p className="channel-description">{selected.description || "Описание канала пока не задано."}</p>
        <div className="detail-grid"><div><span>Участники</span><strong>{participants.filter((item) => item.channelId === selected.id).length}{selected.maxUsers ? ` / ${selected.maxUsers}` : ""}</strong></div><div><span>Кодек</span><strong>{selected.audio.codec.toUpperCase()}</strong></div><div><span>Частота</span><strong>{selected.audio.sampleRate / 1000} кГц</strong></div><div><span>Битрейт</span><strong>{selected.audio.bitrate / 1000} кбит/с</strong></div></div>
        <button className="primary-button join-button" disabled={selected.id === view.channelId || view.connectionStatus !== "connected"} onClick={() => void invoke(() => Service.JoinChannel(selected.id))}>{selected.id === view.channelId ? "Вы уже здесь" : "Войти в канал"}</button>
      </> : <div className="empty-state">Выберите канал</div>}</section>
    </div><EventPanel events={events} onClear={onClearEvents} />
  </section>;
}

function ChannelTree({ channels, participants, selectedID, currentID, onSelect }: { channels: ChannelDTO[]; participants: ParticipantDTO[]; selectedID: string; currentID: string; onSelect: (id: string) => void }) {
  const children = useMemo(() => {
    return buildChannelGroups(channels);
  }, [channels]);
  const renderLevel = (parentID: string, depth: number): React.ReactNode => (children.get(parentID) ?? []).map((channel) => {
    const members = participants.filter((participant) => participant.channelId === channel.id);
    return <div key={channel.id}><button className={`channel-row ${selectedID === channel.id ? "selected" : ""} ${currentID === channel.id ? "current" : ""}`} style={{ paddingLeft: 14 + depth * 18 }} onClick={() => onSelect(channel.id)}><span className="channel-icon" aria-hidden="true">⌁</span><span className="channel-name">{channel.name}</span><span className="channel-count">{members.length}</span></button>
      {members.map((participant) => <ParticipantRow key={participant.sessionId} participant={participant} depth={depth} />)}{renderLevel(channel.id, depth + 1)}</div>;
  });
  return <div className="channel-tree">{channels.length ? renderLevel("0", 0) : <div className="empty-state">Каналы ещё не загружены</div>}</div>;
}

function ParticipantRow({ participant, depth }: { participant: ParticipantDTO; depth: number }) {
  return <div className={`participant-row ${participant.speaking ? "speaking" : ""}`} style={{ paddingLeft: 46 + depth * 18 }}><span className="avatar">{participant.displayName.slice(0, 1).toUpperCase()}</span><span>{participant.displayName}{participant.local ? " (вы)" : ""}</span><span className="speaking-ring" aria-label={participant.speaking ? "Говорит" : "Не говорит"} /></div>;
}

function EventPanel({ events, onClear }: { events: ClientEventDTO[]; onClear: () => void }) {
  const [collapsed, setCollapsed] = useState(false); const listRef = useRef<HTMLDivElement>(null); const stickToBottom = useRef(true); const [unseen, setUnseen] = useState(0); const previousLength = useRef(events.length);
  useEffect(() => { const added = Math.max(0, events.length - previousLength.current); previousLength.current = events.length; if (stickToBottom.current && listRef.current) { listRef.current.scrollTop = listRef.current.scrollHeight; setUnseen(0); } else if (added) setUnseen((value) => value + added); }, [events]);
  const scrollToLatest = () => { if (!listRef.current) return; listRef.current.scrollTop = listRef.current.scrollHeight; stickToBottom.current = true; setUnseen(0); };
  return <section className={`panel event-panel ${collapsed ? "collapsed" : ""}`}><div className="event-heading"><button className="collapse-button" onClick={() => setCollapsed((value) => !value)} aria-expanded={!collapsed}>⌄</button><div><p className="eyebrow">АКТИВНОСТЬ</p><h2>События сервера</h2></div><div className="event-actions">{unseen > 0 && <button className="new-events" onClick={scrollToLatest}>Новых: {unseen}</button>}<button className="text-button" onClick={() => { onClear(); setUnseen(0); }}>Очистить</button></div></div>
    {!collapsed && <div className="event-list" ref={listRef} onScroll={(event) => { const element = event.currentTarget; stickToBottom.current = element.scrollHeight - element.scrollTop - element.clientHeight < 12; if (stickToBottom.current) setUnseen(0); }}>{events.length === 0 ? <div className="empty-event">События появятся после подключения</div> : events.map((item) => <div className={`event-row kind-${item.kind}`} key={item.sequence}><time>{new Date(item.time).toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit", second: "2-digit" })}</time><span className="event-marker" /><span>{item.message}</span></div>)}</div>}
  </section>;
}

function SettingsPage({ view, invoke }: { view: ClientViewDTO; invoke: (operation: () => Promise<unknown>) => Promise<void> }) {
  const [sensitivity, setSensitivity] = useState(view.audio.vadSensitivity); useEffect(() => setSensitivity(view.audio.vadSensitivity), [view.audio.vadSensitivity]);
  const [devices, setDevices] = useState<AudioDevicesDTO | null>(null);
  const [devicesError, setDevicesError] = useState("");
  const [devicePending, setDevicePending] = useState(false);
  const loadDevices = useCallback(async () => {
    try {
      const next = await Service.AudioDevices();
      next.capture ??= [];
      next.playback ??= [];
      setDevices(next);
      setDevicesError("");
    } catch (error) { setDevicesError(errorText(error)); }
  }, []);
  useEffect(() => { void loadDevices(); }, [loadDevices]);
  const selectDevice = async (kind: "capture" | "playback", id: string) => {
    if (devicePending) return;
    setDevicePending(true); setDevicesError("");
    try {
      if (kind === "capture") await Service.SetCaptureDevice(id);
      else await Service.SetPlaybackDevice(id);
      await loadDevices();
    } catch (error) { setDevicesError(errorText(error)); }
    finally { setDevicePending(false); }
  };
  return <section className="settings-page"><div className="settings-heading"><p className="eyebrow">ПАРАМЕТРЫ КЛИЕНТА</p><h2>Настройки</h2></div>
    {devicesError && <div className="device-error" role="alert">Не удалось получить аудиоустройства: {devicesError} <button className="text-button" onClick={() => void loadDevices()}>Повторить</button></div>}
    <section className="settings-card"><h3>Микрофон</h3><DeviceSelect id="capture-device" label="Устройство захвата звука" devices={devices?.capture ?? []} value={devices?.selectedCapture ?? ""} disabled={!devices || devicePending} onChange={(id) => selectDevice("capture", id)} />
      <SettingToggle title="Шумоподавление RNNoise" description="Убирает постоянный фоновый шум до анализа голосовой активности." checked={view.audio.rnnoiseEnabled} onChange={(value) => invoke(() => Service.SetRNNoiseEnabled(value))} />
      <SettingToggle title="Обнаружение голосовой активности (VAD)" description="Микрофон передаёт звук автоматически, когда обнаружена речь." checked={view.audio.vadEnabled} onChange={(value) => invoke(() => Service.SetVADEnabled(value))} />
      <div className="mode-row"><label htmlFor="vad-mode">Режим</label><select id="vad-mode" value={view.audio.vadMode} disabled={!view.audio.vadEnabled} onChange={(event) => void invoke(() => Service.SetVADMode(event.target.value))}><option value="level">По громкости</option><option value="vad">Распознавание речи</option><option value="hybrid">Гибридный</option></select><span className={`gate-indicator ${view.audio.vadOpen ? "open" : ""}`}>{view.audio.vadOpen ? "Передача" : "Ожидание речи"}</span></div>
      <div className="sensitivity-setting"><div><span className="setting-label">Чувствительность</span><output>{Math.round(sensitivity * 100)}%</output></div><input aria-label="Чувствительность VAD" type="range" min="0" max="1" step="0.01" value={sensitivity} disabled={!view.audio.vadEnabled} onChange={(event) => setSensitivity(Number(event.target.value))} onPointerUp={() => void invoke(() => Service.SetVADSensitivity(sensitivity))} onKeyUp={() => void invoke(() => Service.SetVADSensitivity(sensitivity))} /><p>Чем выше значение, тем тише может быть речь, открывающая микрофон.</p></div>
    </section>
    <section className="settings-card compact-card"><h3>Воспроизведение</h3><DeviceSelect id="playback-device" label="Устройство вывода звука" devices={devices?.playback ?? []} value={devices?.selectedPlayback ?? ""} disabled={!devices || devicePending} onChange={(id) => selectDevice("playback", id)} /><SettingToggle title="Заглушить звук" description="Входящий голос продолжает обрабатываться, но не воспроизводится." checked={view.audio.deafened} onChange={(value) => invoke(() => Service.SetDeafened(value))} /></section>
    <section className="settings-card compact-card"><h3>Качество голоса</h3><div className="profile-row"><div><span>Профиль</span><strong>Opus Voice</strong></div><div><span>Формат</span><strong>48 кГц · Mono · 20 мс</strong></div></div></section>
  </section>;
}

function DeviceSelect({ id, label, devices, value, disabled, onChange }: { id: string; label: string; devices: AudioDeviceDTO[]; value: string; disabled: boolean; onChange: (id: string) => Promise<void> }) {
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
  return <div className="device-select"><span className="setting-label" id={`${id}-label`}>{label}</span><div className="device-select-control"><button id={id} className="device-select-trigger" type="button" disabled={disabled} aria-labelledby={`${id}-label ${id}`} aria-haspopup="listbox" aria-expanded={open} onClick={() => setOpen((current) => !current)}><span>{selectedName}</span></button>{open && <div className="device-options" role="listbox" aria-labelledby={`${id}-label`}><button type="button" role="option" aria-selected={value === ""} className={value === "" ? "selected" : ""} onClick={() => choose("")}>Системное устройство по умолчанию</button>{devices.map((device) => <button type="button" role="option" aria-selected={device.id === value} className={device.id === value ? "selected" : ""} onClick={() => choose(device.id)} key={device.id}>{device.name}{device.isDefault ? <small>Системное по умолчанию</small> : null}</button>)}</div>}</div></div>;
}

function VoiceBar({ view, invoke }: { view: ClientViewDTO; invoke: (operation: () => Promise<unknown>) => Promise<void> }) {
  const local = (view.participants ?? []).find((participant) => participant.local);
  const channel = (view.channels ?? []).find((item) => item.id === view.channelId);
  return <footer className="voice-bar">
    <span className={`voice-avatar ${local?.speaking ? "speaking" : ""}`}>{(local?.displayName || "?").slice(0, 1).toUpperCase()}</span>
    <span className="voice-identity"><strong>{local?.displayName || "Подключение…"}</strong><span>{channel?.name || "Без канала"}</span></span>
    <button className={`voice-control ${view.audio.muted ? "active" : ""}`} aria-pressed={view.audio.muted} onClick={() => void invoke(() => Service.SetMuted(!view.audio.muted))}>{view.audio.muted ? "Микрофон выкл." : "Микрофон"}</button>
    <button className={`voice-control ${view.audio.deafened ? "active" : ""}`} aria-pressed={view.audio.deafened} onClick={() => void invoke(() => Service.SetDeafened(!view.audio.deafened))}>{view.audio.deafened ? "Звук выкл." : "Звук"}</button>
  </footer>;
}

function SettingToggle({ title, description, checked, onChange }: { title: string; description: string; checked: boolean; onChange: (value: boolean) => Promise<void> }) {
  return <div className="setting-toggle"><div><strong>{title}</strong><p>{description}</p></div><label className="switch"><input type="checkbox" checked={checked} onChange={(event) => void onChange(event.target.checked)} /><span /></label></div>;
}

export default App;
