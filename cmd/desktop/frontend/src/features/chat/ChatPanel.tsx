import {useEffect, useLayoutEffect, useReducer, useRef, useState} from "react";
import {Icon} from "../../components/Icon";
import {errorText} from "../../locales";
import {useLocale} from "../../locales/Locale";
import {chatLinkParts, openConfirmedLink} from "./chatLinks";
import {ChatStore, type ChatTarget} from "./chatStore";

function ChatMessageText({text}: {text: string}) {
    return chatLinkParts(text).map((part, index) => part.href
        ? <a key={index} href={part.href} onClick={(event) => { event.preventDefault(); openConfirmedLink(part.href!); }}>{part.text}</a>
        : <span key={index}>{part.text}</span>);
}

const resizeComposer = (input: HTMLTextAreaElement) => {
    input.style.height = "0px";
    const contentHeight = input.scrollHeight + 2;
    input.style.height = `${Math.max(32, Math.min(68, contentHeight))}px`;
    input.style.overflowY = contentHeight > 68 ? "auto" : "hidden";
};

export function ChatPanel({store, target, title, active, connected, online, localName}: {
    store: ChatStore; target: ChatTarget; title: string; active: boolean; connected: boolean; online?: boolean;     localName: string;
}) {
    const {m, htmlLang} = useLocale();
    const messageDate = (at: number) => new Date(at).toLocaleString(htmlLang, {day: "2-digit", month: "2-digit", year: "numeric", hour: "2-digit", minute: "2-digit"});
    const [, refresh] = useReducer((n: number) => n + 1, 0);
    const history = useRef<HTMLDivElement>(null);
    const composer = useRef<HTMLTextAreaElement | null>(null);
    const atBottom = useRef(true);
    const previousHeight = useRef<number | null>(null);
    const previousAnchor = useRef<{id: string; top: number} | null>(null);
    const [newMessages, setNewMessages] = useState(false);
    const [visible, setVisible] = useState(document.visibilityState === "visible");
    useEffect(() => store.subscribe(refresh), [store]);
    useEffect(() => {
        const changed = () => setVisible(document.visibilityState === "visible");
        document.addEventListener("visibilitychange", changed);
        return () => document.removeEventListener("visibilitychange", changed);
    }, []);
    const c = store.conversation(target);
    useLayoutEffect(() => {
        if (!active || !composer.current) return;
        const input = composer.current;
        const resize = () => {
            resizeComposer(input);
            if (atBottom.current && history.current) history.current.scrollTop = history.current.scrollHeight;
        };
        resize();
        let width = input.clientWidth;
        const observer = new ResizeObserver(() => {
            if (input.clientWidth === width) return;
            width = input.clientWidth;
            resize();
        });
        observer.observe(input);
        return () => observer.disconnect();
    }, [c.draft, active]);
    const lastID = c.messages[c.messages.length - 1]?.id;
    useEffect(() => {
        if (!active || !connected || !visible) return;
        return store.watch(target);
    }, [store, target.kind, target.id, active, connected, visible]);
    useLayoutEffect(() => {
        if (!active || !history.current) return;
        if (previousHeight.current !== null && c.loading) return;
        if (previousHeight.current !== null) {
            const anchor = previousAnchor.current;
            const node = anchor ? history.current.querySelector<HTMLElement>(`[data-message-id="${anchor.id}"]`) : null;
            history.current.scrollTop += node && anchor ? node.getBoundingClientRect().top - anchor.top : history.current.scrollHeight - previousHeight.current;
            previousHeight.current = null;
            previousAnchor.current = null;
        } else if (atBottom.current) history.current.scrollTop = history.current.scrollHeight;
        else setNewMessages(true);
    }, [lastID, c.messages[0]?.id, c.messages.length, c.pending.length, active]);
    useEffect(() => {
        if (active && connected && visible && atBottom.current) void store.markRead(target);
    }, [lastID, c.loading, active, connected, visible, store, target.id, target.kind]);
    const bytes = new TextEncoder().encode(c.draft).length;
    const valid = c.draft.trim().length > 0 && bytes <= 1000 && !c.draft.includes("\0");
    const send = () => { if (connected && valid) void store.send(target, c.draft); };
    const older = async () => {
        if (c.loading) return;
        const first = c.messages[0]?.id, last = c.messages[c.messages.length - 1]?.id;
        previousHeight.current = history.current?.scrollHeight ?? null;
        const top = history.current?.getBoundingClientRect().top ?? 0;
        const anchor = [...(history.current?.querySelectorAll<HTMLElement>("[data-message-id]") ?? [])].find((node) => node.getBoundingClientRect().bottom > top);
        previousAnchor.current = anchor ? {id: anchor.dataset.messageId!, top: anchor.getBoundingClientRect().top} : null;
        await store.sync(target, true);
        // A failed/no-op load must not affect the next incoming message's scroll.
        if (first === c.messages[0]?.id && last === c.messages[c.messages.length - 1]?.id) {
            previousHeight.current = null; previousAnchor.current = null;
        }
        refresh();
    };
    return <section className="chat-panel" aria-label={title}>
        {target.kind === "direct" && <header className="chat-heading"><strong>{title}</strong><span>{online ? m.chat.online : m.chat.offline}</span></header>}
        {c.error && <div className="chat-error" role="alert">{errorText(c.error, m)}<button onClick={() => void store.sync(target)} disabled={!connected || c.loading}>{m.chat.retry}</button></div>}
        <div ref={history} className="chat-history" aria-label={m.chat.history} onScroll={() => {
            const node = history.current;
            if (!node) return;
            atBottom.current = node.scrollHeight - node.scrollTop - node.clientHeight < 40;
            if (atBottom.current) {
                setNewMessages(false);
                if (active && connected && visible) void store.markRead(target);
            }
            if (active && connected && node.scrollTop < 20 && node.scrollHeight > node.clientHeight && c.hasOlder && !c.loading) void older();
        }}>
            {c.hasOlder && <button className="chat-older" disabled={!connected || c.loading} onClick={() => void older()}>{m.chat.older}</button>}
            {!c.loaded && <div className="chat-placeholder"><Icon name="chat"/><p>{c.loading ? m.chat.loading : connected ? m.chat.openToLoad : m.chat.waiting}</p></div>}
            {c.loaded && !c.messages.length && !c.pending.length && <div className="chat-placeholder">{m.chat.empty}</div>}
            {c.messages.map((message) => <article key={message.id} data-message-id={message.id} className={`chat-message ${message.senderId === store.userId ? "own" : ""}`}>
                <time dateTime={new Date(message.sentAtMs).toISOString()}>{messageDate(message.sentAtMs)}</time>{" "}<strong>{message.senderName}</strong>{": "}<ChatMessageText text={message.text}/>
            </article>)}
            {c.pending.map((p) => <article key={p.clientId} className="chat-message own pending">
                <time dateTime={new Date(p.sentAtMS).toISOString()}>{messageDate(p.sentAtMS)}</time>{" "}<strong>{localName}</strong>{": "}<ChatMessageText text={p.text}/>{" "}
                {p.state === "sending" ? <span className="chat-send-status" role="status" aria-label={m.chat.sending} title={m.chat.sending}>…</span> : <button disabled={!connected} title={errorText(p.error ?? "", m)} aria-label={m.chat.retrySend(errorText(p.error ?? "chat-send-failed", m))} onClick={() => void store.send(target, p.text, p)}>{m.chat.retry}</button>}
            </article>)}
        </div>
        {(newMessages || c.olderWindow || c.newMessages) && <button className="chat-new" disabled={!connected || c.loading} onClick={async () => {
            atBottom.current = true;
            if (c.olderWindow) await store.latest(target);
            if (history.current) history.current.scrollTop = history.current.scrollHeight;
            atBottom.current = true; setNewMessages(false);
            if (active && connected && visible) void store.markRead(target);
        }}>{m.chat.jump}</button>}
        <div className="chat-composer">
            <textarea ref={composer} rows={1} maxLength={1000} aria-label={m.chat.message} title={m.chat.composerHint} placeholder={connected ? m.chat.placeholder : m.chat.draftWaiting} value={c.draft}
                onChange={(event) => store.setDraft(target, event.target.value)}
                onKeyDown={(event) => { if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing && event.keyCode !== 229) { event.preventDefault(); send(); } }}/>
            <small title={m.chat.bytes}>{bytes}/1000</small>
            <button disabled={!connected || !valid} type="button" onClick={send}>{m.chat.send}</button>
        </div>
    </section>;
}
