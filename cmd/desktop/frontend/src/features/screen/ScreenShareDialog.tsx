import {useEffect, useRef, useState} from "react";
import type {ClientViewDTO} from "../../api";
import {logDiagnostic} from "../../api";
import {ScreenMediaController, senderCodecAvailable} from "./screenMedia";
import {loadScreenSettings, screenBitrates, type ScreenSettings} from "./screenProfiles";
import {errorText} from "../../locales";
import {useLocale} from "../../locales/Locale";

export function useScreenSharing(view: ClientViewDTO, controller: ScreenMediaController, onError: (message: string) => void) {
    const [open, setOpen] = useState(false);
    const [pending, setPending] = useState(false);
    const [publishing, setPublishing] = useState(false);
    const [error, setError] = useState("");
    const [settings, setSettings] = useState<ScreenSettings>(loadScreenSettings);
    const updateSettings = (next: ScreenSettings) => {
        setSettings(next);
        try { localStorage.setItem("sonoryx.screenSettings", JSON.stringify(next)); } catch { /* Session choice survives. */ }
    };
    useEffect(() => { try { localStorage.setItem("sonoryx.screenSettings", JSON.stringify(settings)); } catch { /* Storage may be disabled. */ } }, [settings]);
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
        busy.current = true; setPending(true); setError(""); setOpen(false);
        const startedIn = context;
        try {
            await controller.publish(() => { if (currentContext.current === startedIn) setPublishing(false); }, settings, () => currentContext.current === startedIn);
            if (currentContext.current === startedIn) { setPublishing(true); setOpen(false); }
        } catch (reason) {
            const cancelled = reason instanceof DOMException && ["NotAllowedError", "AbortError"].includes(reason.name)
                || /permission denied by user|user cancel(?:led|ed)/i.test(String(reason));
            if (currentContext.current === startedIn) {
                setOpen(true);
                if (!cancelled) setError(reason instanceof Error ? reason.message : String(reason));
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
    return {open, pending, active, available, settings, selectSettings, error, buttonRef, close, start, toggle};
}

export type ScreenSharingState = ReturnType<typeof useScreenSharing>;

export function ScreenShareDialog({sharing}: {sharing: ScreenSharingState}) {
    const {m} = useLocale();
    const dialogRef = useRef<HTMLDialogElement>(null);
    const restoreFocus = useRef(false);
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
            <label>{m.screen.settings.codec} <select value={sharing.settings.codec} onChange={(e) => sharing.selectSettings({...sharing.settings, codec: e.target.value as ScreenSettings["codec"]})}>
                {["auto","VP8","H264","VP9","AV1"].map((codec) => <option key={codec} value={codec} disabled={codec !== "auto" && !senderCodecAvailable(codec)}>{codec === "auto" ? m.screen.settings.auto : codec === "H264" ? "H.264" : codec}{codec !== "auto" && !senderCodecAvailable(codec) ? ` (${m.screen.settings.unavailable})` : ""}</option>)}
            </select></label>
            <label>{m.screen.settings.content} <select value={sharing.settings.content} onChange={(e) => sharing.selectSettings({...sharing.settings, content: e.target.value as ScreenSettings["content"]})}>
                <option value="detail">{m.screen.settings.detail}</option><option value="motion">{m.screen.settings.motion}</option>
            </select></label>
            <label>{m.connection.bitrate} <select value={sharing.settings.bitrate} onChange={(e) => sharing.selectSettings({...sharing.settings, bitrate: Number(e.target.value)})}>
                {screenBitrates.map((bitrate) => <option key={bitrate} value={bitrate}>{bitrate / 1_000_000} Mbps</option>)}
            </select></label>
        </fieldset>
        <p className="screen-capability-note">{m.screen.settings.capabilities}</p>
        {sharing.error && <p className="screen-error" role="alert">{errorText(sharing.error, m)}</p>}
        <div className="screen-dialog-actions">
            <button type="button" onClick={sharing.close}>{m.screen.cancel}</button>
            <button className="primary-button" type="button" disabled={sharing.pending || !sharing.available}
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
