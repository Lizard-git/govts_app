import {useEffect, useState} from "react";
import type {ClientEventDTO, ClientViewDTO} from "../../api";
import {errorText} from "../../locales";
import {useLocale} from "../../locales/Locale";
import {EventPanel} from "./EventPanel";

export function ConnectionProgress({view, events}: {
    view: ClientViewDTO;
    events: ClientEventDTO[];
}) {
    const {m} = useLocale();
    const [startedAt] = useState(Date.now);
    const [elapsed, setElapsed] = useState(0);
    const connected = view.connectionStatus === "connected";
    const reconnecting = view.connectionStatus === "reconnecting";
    useEffect(() => {
        if (connected && view.snapshotFresh) return;
        const timer = window.setInterval(() => setElapsed(Math.floor((Date.now() - startedAt) / 1000)), 1000);
        return () => window.clearInterval(timer);
    }, [startedAt, connected, view.snapshotFresh]);
    return <section className="connection-progress-wrap" aria-label={m.connection.statsRegion}>
        <div className="connection-progress">
            <div role="status" className="connection-progress-heading">
                <h1><span className={connected ? "connection-progress-dot connected" : "connection-progress-dot"} aria-hidden="true"/>{connected ? (view.snapshotFresh ? m.channels.waitingChannel : m.connection.receivingState) : reconnecting ? m.connection.connectionLost : m.connection.awaitingServer}</h1>
                <p>{connected ? (view.snapshotFresh ? m.connection.selectChannelHint : m.connection.receivingStateHint) : reconnecting ? m.connection.connectionLostHint : m.connection.waitingServerHint}</p>
            </div>
            {(!connected || !view.snapshotFresh) && <span className="connection-progress-elapsed">{m.connection.elapsed} {Math.floor(elapsed / 60)}:{String(elapsed % 60).padStart(2, "0")}</span>}
            {view.lastError && <div className="form-error" role="alert">{errorText(view.lastError, m)}</div>}
        </div>
        <div className="connection-progress-log"><h2>{m.channels.eventLog}</h2><EventPanel events={events} active/></div>
    </section>;
}
