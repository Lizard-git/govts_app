import {useEffect, useRef, useState} from "react";
import {useLocale} from "../../locales/Locale";

// The first use of a media server pins its TLS fingerprint. The user confirms
// it here instead of the client trusting whatever key answers first.

type PendingTrust = {fingerprint: string; resolve: (accepted: boolean) => void};

let pending: PendingTrust | null = null;
const listeners = new Set<() => void>();

function publish() {
    for (const listener of listeners) listener();
}

function settle(accepted: boolean) {
    const current = pending;
    if (!current) return;
    pending = null;
    current.resolve(accepted);
    publish();
}

export function confirmMediaServer(fingerprint: string): Promise<boolean> {
    if (pending) pending.resolve(false);
    return new Promise((resolve) => {
        pending = {fingerprint, resolve};
        publish();
    });
}

function KeyMark() {
    return <svg viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <circle cx="7.5" cy="15.5" r="4.5"/>
        <path d="m10.7 12.3 9.3-9.3"/>
        <path d="m16 7 3 3"/>
        <path d="m19 4 2 2"/>
    </svg>;
}

export function MediaTrustDialog() {
    const {m} = useLocale();
    const dialogRef = useRef<HTMLDialogElement>(null);
    const shownRef = useRef("");
    const [fingerprint, setFingerprint] = useState<string | null>(null);
    if (fingerprint) shownRef.current = fingerprint;
    useEffect(() => {
        const sync = () => setFingerprint(pending?.fingerprint ?? null);
        listeners.add(sync);
        sync();
        return () => { listeners.delete(sync); };
    }, []);
    useEffect(() => {
        const dialog = dialogRef.current;
        if (!dialog) return;
        if (fingerprint && !dialog.open) {
            dialog.showModal();
            // Cancel is the safe default: Enter must not trust an unknown key.
            dialog.querySelector<HTMLButtonElement>("button:not(.primary-button)")?.focus();
        } else if (!fingerprint && dialog.open) dialog.close();
    }, [fingerprint]);
    return <dialog ref={dialogRef} className="link-confirm" aria-labelledby="media-trust-title" aria-describedby="media-trust-text"
        onCancel={() => settle(false)}
        onClose={() => settle(false)}
        onClick={(event) => { if (event.target === event.currentTarget) dialogRef.current?.close(); }}>
        {(fingerprint ?? shownRef.current) && <>
            <div className="link-confirm-mark"><KeyMark/></div>
            <h2 id="media-trust-title">{m.screen.mediaTrustTitle}</h2>
            <p id="media-trust-text">{m.screen.mediaTrustText}</p>
            <p className="link-confirm-address"><code>{fingerprint ?? shownRef.current}</code></p>
            <div className="screen-dialog-actions">
                <button type="button" onClick={() => settle(false)}>{m.screen.mediaTrustCancel}</button>
                <button className="primary-button" type="button" onClick={() => settle(true)}>{m.screen.mediaTrustAccept}</button>
            </div>
        </>}
    </dialog>;
}
