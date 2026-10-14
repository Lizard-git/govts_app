import {useEffect, useRef, useState} from "react";
import type {ClientViewDTO} from "../../api";
import {desktopAPI, logDiagnostic} from "../../api";
import {ScreenMediaController, ScreenCodecLimitError, senderCodecAvailable, senderH264Level, h264SettingsFit, h264SupportedSettings} from "./screenMedia";
import {loadScreenSettings, screenBitrates, type ScreenSettings} from "./screenProfiles";
import {errorText} from "../../locales";
import {useLocale} from "../../locales/Locale";

export function useScreenSharing(view: ClientViewDTO, controller: ScreenMediaController, onError: (message: string) => void) {
    const [open, setOpen] = useState(false);
    const [pending, setPending] = useState(false);
    const [publishing, setPublishing] = useState(false);
    const [error, setError] = useState("");
    const [codecLimit, setCodecLimit] = useState<number | null>(null);
    const [settings, setSettings] = useState<ScreenSettings>(loadScreenSettings);
    const updateSettings = (next: ScreenSettings) => {
        setSettings(next);
        setError("");
        setCodecLimit(null);
        try { localStorage.setItem("sonoryx.screenSettings", JSON.stringify(next)); } catch { /* Session choice survives. */ }
    };
    const buttonRef = useRef<HTMLButtonElement>(null);
    const busy = useRef(false);
    const context = `${view.sessionId}:${view.channelId}:${view.connectionStatus}`;
    const currentContext = useRef(context);
    currentContext.current = context;
    const available = view.connectionStatus === "connected" && view.channelId !== "0";
    const own = (view.screenStreams ?? []).some((stream) => stream.ownerSessionId === view.sessionId);
    const active = publishing || own;

    useEffect(() => {
        setOpen(false);
        setError("");
        setCodecLimit(null);
        setPublishing(false);
        return () => {
            void controller.stopPublishing().catch((reason) => logDiagnostic("screen_context_cleanup", reason));
        };
    }, [context, controller]);

    const close = () => {
        setOpen(false);
        if (busy.current) void controller.stopPublishing().catch((reason) => logDiagnostic("screen_cancel_cleanup", reason));
    };
    const selectSettings = updateSettings;
    const start = async () => {
        if (busy.current || !available) return;
        busy.current = true; setPending(true); setError(""); setCodecLimit(null); setOpen(false);
        const startedIn = context;
        try {
            await controller.publish(() => { if (currentContext.current === startedIn) setPublishing(false); }, settings, () => currentContext.current === startedIn);
            if (currentContext.current === startedIn) { setPublishing(true); setOpen(false); }
        } catch (reason) {
            const cancelled = reason instanceof DOMException && ["NotAllowedError", "AbortError"].includes(reason.name)
                || /permission denied by user|user cancel(?:led|ed)/i.test(String(reason));
            if (currentContext.current === startedIn) {
                setOpen(true);
                if (!cancelled) {
                    if (reason instanceof ScreenCodecLimitError) setCodecLimit(reason.level);
                    else setError(reason instanceof Error ? reason.message : String(reason));
                }
            }
        } finally { busy.current = false; setPending(false); }
    };
    const toggle = async () => {
        if (busy.current || !available) return;
        if (!active) { setError(""); setOpen(true); return; }
        busy.current = true; setPending(true);
        try { await controller.stopPublishing(); }
        catch (reason) { onError(reason instanceof Error ? reason.message : String(reason)); }
        finally { setPublishing(false); busy.current = false; setPending(false); }
    };
    return {open, pending, active, available, settings, selectSettings, error, codecLimit, buttonRef, close, start, toggle};
}

export type ScreenSharingState = ReturnType<typeof useScreenSharing>;

export function ScreenShareDialog({sharing}: {sharing: ScreenSharingState}) {
    const {m} = useLocale();
    const dialogRef = useRef<HTMLDialogElement>(null);
    const restoreFocus = useRef(false);
    const [serverCodecs, setServerCodecs] = useState<Awaited<ReturnType<typeof desktopAPI.screenCapabilities>>>();
    const [checkingCodecs, setCheckingCodecs] = useState(false);
    useEffect(() => {
        if (!sharing.open) return;
        let cancelled = false;
        setServerCodecs(undefined);
        setCheckingCodecs(true);
        void desktopAPI.screenCapabilities().then((result) => {
            if (!cancelled) setServerCodecs(result);
        }).catch(() => { /* Publishing performs the authenticated check again. */ }).finally(() => {
            if (!cancelled) setCheckingCodecs(false);
        });
        return () => { cancelled = true; };
    }, [sharing.open]);
    const h264Level = sharing.codecLimit ?? senderH264Level(serverCodecs);
    const h264Options = h264SupportedSettings(h264Level);
    const h264Selected = sharing.settings.codec === "H264";
    const incompatible = h264Selected && !checkingCodecs && h264SettingsFit(sharing.settings, h264Level) === false;
    const codecWarning = incompatible || sharing.codecLimit !== null;
    const currentHeight = sharing.settings.resolution === "native" ? Infinity : Number(sharing.settings.resolution.slice(0, -1));
    const suggestion = [...h264Options].reverse().find((option) => Number(option.resolution.slice(0, -1)) <= currentHeight);
    const useSuggestion = () => {
        if (suggestion) sharing.selectSettings({...sharing.settings, resolution: suggestion.resolution, fps: Math.min(sharing.settings.fps, suggestion.fps) as ScreenSettings["fps"]});
    };
    useEffect(() => {
        const dialog = dialogRef.current;
        if (!dialog) return;
        if (sharing.open && !dialog.open) dialog.showModal();
        else if (!sharing.open && dialog.open) {
            dialog.close();
            restoreFocus.current = true;
        }
        if (!sharing.open && !sharing.pending && restoreFocus.current) {
            sharing.buttonRef.current?.focus();
            restoreFocus.current = false;
        }
    }, [sharing.open, sharing.pending, sharing.buttonRef]);
    return <dialog ref={dialogRef} className="screen-share-dialog" aria-labelledby="screen-share-title"
        onCancel={(event) => { event.preventDefault(); sharing.close(); }}>
        <h2 id="screen-share-title">{m.screen.title}</h2>
        <p>{m.screen.pick}</p>
        <fieldset className="screen-settings" disabled={sharing.pending}>
            <label>FPS <select value={sharing.settings.fps} onChange={(e) => sharing.selectSettings({...sharing.settings, fps: Number(e.target.value) as ScreenSettings["fps"]})}>
                {[10,15,30,60].map((fps) => <option key={fps}>{fps}</option>)}
            </select></label>
            <label>{m.connection.resolution} <select value={sharing.settings.resolution} onChange={(e) => sharing.selectSettings({...sharing.settings, resolution: e.target.value as ScreenSettings["resolution"]})}>
                {["720p","1080p","1440p","2160p","native"].map((size) => <option key={size} value={size}>{size === "native" ? m.screen.settings.native : size}</option>)}
            </select></label>
            <label>{m.screen.settings.codec} <select aria-describedby="screen-codec-help" value={sharing.settings.codec} onChange={(e) => sharing.selectSettings({...sharing.settings, codec: e.target.value as ScreenSettings["codec"]})}>
                {["auto","VP8","H264","VP9","AV1"].map((codec) => <option key={codec} value={codec} disabled={codec !== "auto" && !senderCodecAvailable(codec)}>{codec === "auto" ? m.screen.settings.auto : codec === "H264" ? "H.264" : codec}{codec !== "auto" && !senderCodecAvailable(codec) ? ` (${m.screen.settings.unavailable})` : ""}</option>)}
            </select></label>
            <label>{m.screen.settings.content} <select value={sharing.settings.content} onChange={(e) => sharing.selectSettings({...sharing.settings, content: e.target.value as ScreenSettings["content"]})}>
                <option value="detail">{m.screen.settings.detail}</option><option value="motion">{m.screen.settings.motion}</option>
            </select></label>
            <label>{m.connection.bitrate} <select value={sharing.settings.bitrate} onChange={(e) => sharing.selectSettings({...sharing.settings, bitrate: Number(e.target.value)})}>
                {screenBitrates.map((bitrate) => <option key={bitrate} value={bitrate}>{bitrate / 1_000_000} Mbps</option>)}
            </select></label>
        </fieldset>
        <div id="screen-codec-help" className={`screen-codec-help${codecWarning ? " screen-codec-warning" : ""}`} aria-live="polite">
            {checkingCodecs ? <p>{m.screen.compatibility.checking}</p> : <>
                <p>{sharing.settings.codec === "auto" ? m.screen.compatibility.auto : sharing.settings.codec === "VP8" ? m.screen.compatibility.vp8 : h264Selected ? m.screen.compatibility.h264 : m.screen.compatibility.other}</p>
                {h264Selected && h264Options.length > 0 && <p>{serverCodecs ? m.screen.compatibility.connectionLimits : m.screen.compatibility.clientLimits} {h264Options.map((option) => `${option.resolution} / ${option.fps} FPS`).join(" · ")}</p>}
                {h264Selected && sharing.settings.resolution === "native" && <p>{m.screen.compatibility.native}</p>}
                {!serverCodecs && <p>{m.screen.settings.capabilities}</p>}
            </>}
            {codecWarning && <>
                <p role="alert">{!h264Level ? m.screen.compatibility.unsupported : sharing.codecLimit !== null ? m.screen.compatibility.captureTooLarge : m.screen.compatibility.tooLarge}</p>
                <div className="screen-codec-actions">
                    {suggestion && <button type="button" disabled={sharing.pending} onClick={useSuggestion}>{m.screen.compatibility.use} {suggestion.resolution} / {Math.min(sharing.settings.fps, suggestion.fps)} FPS</button>}
                    <button type="button" disabled={sharing.pending} onClick={() => sharing.selectSettings({...sharing.settings, codec: "auto"})}>{m.screen.compatibility.useAuto}</button>
                </div>
            </>}
        </div>
        <p className="screen-capability-note">{m.screen.compatibility.bitrate}</p>
        {sharing.error && <p className="screen-error" role="alert">{errorText(sharing.error, m)}</p>}
        <div className="screen-dialog-actions">
            <button type="button" onClick={sharing.close}>{m.screen.cancel}</button>
            <button className="primary-button" type="button" disabled={sharing.pending || !sharing.available || incompatible || sharing.codecLimit !== null}
                onClick={() => {
                    // Remove the modal top layer before WebView2 opens its capture
                    // picker, while keeping getDisplayMedia in the click call chain.
                    dialogRef.current?.close();
                    restoreFocus.current = true;
                    void sharing.start();
                }}>{sharing.pending ? m.screen.starting : m.screen.start}</button>
        </div>
    </dialog>;
}
