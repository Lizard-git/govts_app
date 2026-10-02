import {desktopAPI, type ChatDialogDTO, type ChatMessageDTO, type ChatPageDTO, type ChatRequestDTO} from "../../api";

export type ChatTarget = {kind: "channel" | "direct"; id: string};
export type PendingMessage = {clientId: string; text: string; sentAtMS: number; state: "sending" | "error"; error?: string};
export type Conversation = {
    messages: ChatMessageDTO[]; pending: PendingMessage[]; draft: string;
    loaded: boolean; loading: boolean; error: string; hasOlder: boolean;
    cursor: string; readId: string; unread: number; olderWindow: boolean;
};
const keyOf = (t: ChatTarget) => `${t.kind}:${t.id}`;
const compare = (a: string, b: string) => BigInt(a) < BigInt(b) ? -1 : BigInt(a) > BigInt(b) ? 1 : 0;
const maxID = (a: string, b: string) => compare(a, b) > 0 ? a : b;
const limitDraft = (text: string): string => {
    const encoder = new TextEncoder();
    let result = "", bytes = 0;
    for (const character of text) {
        const size = encoder.encode(character).length;
        if (bytes + size > 1000) break;
        result += character; bytes += size;
    }
    return result;
};
export const chatError = (e: unknown) => (e instanceof Error ? e.message : String(e)).replace(/^Error:\s*/, "");

export class ChatStore {
    readonly context: string;
    readonly userId: string;
    dialogs: ChatDialogDTO[] = [];
    dialogError = "";
    dialogHasMore = false;
    private dialogCursor = "0";
    private listeners = new Set<() => void>();
    private conversations = new Map<string, Conversation>();
    private busy = new Set<string>();
    private reading = new Set<string>();
    private listing = false;
    private disposed = false;

    constructor(context: string, userId: string) { this.context = context; this.userId = userId; }
    dispose() { this.disposed = true; this.listeners.clear(); }
    subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
    private notify() { if (!this.disposed) for (const listener of this.listeners) listener(); }

    conversation(target: ChatTarget): Conversation {
        const key = keyOf(target);
        let c = this.conversations.get(key);
        if (!c) {
            while (this.conversations.size >= 64) {
                const evict = [...this.conversations.entries()].find(([id, value]) => !this.busy.has(id) && !value.pending.length && !value.draft);
                if (evict) this.conversations.delete(evict[0]);
                else break; // Only pinned drafts, sends and in-flight requests remain.
            }
            c = {messages: [], pending: [], draft: "", loaded: false, loading: false, error: "", hasOlder: false, cursor: "0", readId: "0", unread: 0, olderWindow: false};
            this.conversations.set(key, c);
        }
        // Touch entries so old channel histories are evicted before visible tabs.
        this.conversations.delete(key); this.conversations.set(key, c);
        return c;
    }
    setDraft(target: ChatTarget, text: string) {
        const c = this.conversation(target);
        if (!c.draft && text && [...this.conversations.values()].filter((v) => v.draft).length >= 32) c.error = "Сохранено 32 черновика; отправьте или очистите один из них";
        else c.draft = limitDraft(text);
        this.notify();
    }

    private async request(operation: string, target?: ChatTarget, extra: Partial<ChatRequestDTO> = {}): Promise<ChatPageDTO> {
        if (this.disposed) throw new Error("Контекст чата закрыт");
        const page = await desktopAPI.chat({context: this.context, operation, kind: target?.kind ?? "", targetId: target?.id ?? "0", cursor: "0", forward: false, clientId: "", text: "", ...extra});
        if (this.disposed || page.userId !== this.userId) throw new Error("Учётная запись чата изменилась");
        return page;
    }
    private merge(c: Conversation, page: ChatPageDTO, history = false, older = false) {
        const messages = new Map(c.messages.map((m) => [m.id, m]));
        for (const m of page.messages ?? []) messages.set(m.id, m);
        const sorted = [...messages.values()].sort((a, b) => compare(a.id, b.id));
        if (older && sorted.length > 500) { c.messages = sorted.slice(0, 500); c.olderWindow = true; }
        else c.messages = sorted.slice(-500);
        const confirmed = new Set(c.messages.filter((m) => m.senderId === this.userId).map((m) => m.clientId));
        c.pending = c.pending.filter((p) => !confirmed.has(p.clientId));
        c.readId = maxID(c.readId, page.readId);
        if (history) c.unread = page.unread;
    }

    async sync(target: ChatTarget, older = false) {
        if (target.id === "0" || this.disposed) return;
        const key = keyOf(target);
        if (this.busy.has(key)) return;
        const c = this.conversation(target);
        this.busy.add(key); c.loading = true; this.notify();
        try {
            if (!c.loaded || older) {
                let cursor = older ? c.messages[0]?.id ?? "0" : "0";
                for (let i = 0; i < 4; i++) {
                    const page = await this.request("history", target, {cursor});
                    this.merge(c, page, true, older);
                    c.hasOlder = page.hasMore;
                    if (!c.loaded) c.cursor = (page.messages ?? []).reduce((id, m) => maxID(id, m.id), c.cursor);
                    c.loaded = true;
                    if (!page.hasMore) break;
                    cursor = page.cursor;
                }
            } else {
                for (let i = 0; i < 8; i++) {
                    const page = await this.request("history", target, {cursor: c.cursor, forward: true});
                    this.merge(c, page, true);
                    c.cursor = maxID(c.cursor, page.cursor);
                    if (!page.hasMore) break;
                }
            }
            c.error = "";
        } catch (e) { if (!this.disposed) c.error = chatError(e); }
        finally { c.loading = false; this.busy.delete(key); this.notify(); }
    }

    async refreshDialogs(more = false) {
        if (this.listing || this.disposed) return;
        this.listing = true;
        try {
            const result = new Map<string, ChatDialogDTO>(this.dialogs.map((d) => [d.userId, d]));
            let cursor = more || this.dialogHasMore ? this.dialogCursor : "0";
            for (let i = 0; i < 4; i++) {
                const page = await this.request("dialogs", undefined, {cursor});
                for (const d of page.dialogs ?? []) result.set(d.userId, d);
                this.dialogHasMore = page.hasMore;
                this.dialogCursor = page.cursor;
                if (!page.hasMore) break;
                cursor = page.cursor;
            }
            this.dialogs = [...result.values()].sort((a, b) => compare(b.latestId, a.latestId)).slice(0, 256);
            this.dialogError = "";
        } catch (e) { if (!this.disposed) this.dialogError = chatError(e); }
        finally { this.listing = false; this.notify(); }
    }

    async latest(target: ChatTarget) {
        const c = this.conversation(target);
        if (this.busy.has(keyOf(target))) return;
        c.messages = []; c.loaded = false; c.olderWindow = false; c.hasOlder = false; c.cursor = "0";
        await this.sync(target);
    }

    async dialogPage(cursor: string) {
        const dialogs: ChatDialogDTO[] = [];
        let hasMore = false;
        for (let i = 0; i < 4; i++) {
            const page = await this.request("dialogs", undefined, {cursor});
            dialogs.push(...(page.dialogs ?? []));
            cursor = page.cursor; hasMore = page.hasMore;
            if (!hasMore) break;
        }
        return {dialogs, cursor, hasMore};
    }

    async send(target: ChatTarget, text: string, retry?: PendingMessage) {
        const c = this.conversation(target);
        if (retry?.state === "sending") return;
        if (!retry && [...this.conversations.values()].reduce((sum, v) => sum + v.pending.length, 0) >= 32) {
            c.error = "Слишком много неподтверждённых сообщений"; this.notify(); return;
        }
        const pending: PendingMessage = retry ?? {clientId: crypto.randomUUID().replace(/-/g, ""), text, sentAtMS: Date.now(), state: "sending"};
        pending.state = "sending"; pending.error = undefined;
        if (!retry) { c.pending.push(pending); c.draft = ""; }
        this.notify();
        try { this.merge(c, await this.request("send", target, {clientId: pending.clientId, text: pending.text})); c.error = ""; }
        catch (e) { if (!this.disposed) { pending.state = "error"; pending.error = chatError(e); } }
        finally { this.notify(); }
        void this.refreshDialogs();
    }

    async markRead(target: ChatTarget) {
        const key = keyOf(target), c = this.conversation(target);
        const last = c.messages[c.messages.length - 1]?.id ?? "0";
        const id = compare(last, c.cursor) < 0 ? last : c.cursor;
        if (this.disposed || this.reading.has(key) || compare(id, c.readId) <= 0) return;
        this.reading.add(key);
        try {
            await this.request("read", target, {cursor: id});
            c.readId = maxID(c.readId, id); c.unread = c.messages.filter((m) => m.senderId !== this.userId && compare(m.id, id) > 0).length; this.notify();
            void this.refreshDialogs();
        } catch (e) { if (!this.disposed) { c.error = chatError(e); this.notify(); } }
        finally { this.reading.delete(key); }
    }
}
