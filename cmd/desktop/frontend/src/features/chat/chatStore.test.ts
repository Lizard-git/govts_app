import {afterEach, beforeEach, describe, expect, it, vi} from "vitest";
import {desktopAPI, type ChatMessageDTO, type ChatPageDTO, type ChatRequestDTO} from "../../api";
import {ChatStore, type ChatTarget} from "./chatStore";

vi.mock("../../api", () => ({desktopAPI: {chat: vi.fn()}}));
const channel: ChatTarget = {kind: "channel", id: "1"};
const direct: ChatTarget = {kind: "direct", id: "2"};
const message = (id: number, senderId = "2", clientId = `client-${id}`): ChatMessageDTO => ({
    id: String(id), senderId, recipientId: "1", channelId: "0", clientId, senderName: "bob", text: `message ${id}`, sentAtMs: id,
});
const page = (extra: Partial<ChatPageDTO> = {}): ChatPageDTO => ({userId: "1", latestId: "0", readId: "0", cursor: "0", unread: 0, hasMore: false, messages: [], dialogs: [], ...extra});
let store: ChatStore;
// Store only awaits the bridge promise; cancellation is owned by the Go session.
const chat = vi.mocked(desktopAPI.chat as (request: ChatRequestDTO) => Promise<ChatPageDTO>);
const tick = () => (vi.mocked(window.setInterval).mock.calls[0][0] as () => void)();

beforeEach(() => {
    vi.useFakeTimers(); vi.setSystemTime(new Date("2026-10-02T12:00:00Z"));
    vi.stubGlobal("window", {setInterval: vi.fn(() => 1), clearInterval: vi.fn(), setTimeout: globalThis.setTimeout, clearTimeout: globalThis.clearTimeout});
    chat.mockReset(); chat.mockResolvedValue(page());
    store = new ChatStore("context", "1"); store.setConnected(true);
});
afterEach(() => { store.dispose(); vi.useRealTimers(); vi.unstubAllGlobals(); });

describe("chat drafts and confirmations", () => {
    it("limits UTF-8 bytes without cutting emoji and preserves newlines", () => {
        store.setDraft(channel, "я".repeat(600));
        expect(store.conversation(channel).draft).toBe("я".repeat(500));
        store.setDraft(channel, "🙂".repeat(251));
        expect(store.conversation(channel).draft).toBe("🙂".repeat(250));
        store.setDraft(channel, "first\nsecond");
        expect(store.conversation(channel).draft).toBe("first\nsecond");
    });
    it("retries with the original clientId and removes confirmed pending messages", async () => {
        chat.mockRejectedValueOnce(new Error("lost acknowledgement"));
        await store.send(direct, "hello");
        const pending = store.conversation(direct).pending[0];
        expect(pending.state).toBe("error");
        chat.mockResolvedValueOnce(page({messages: [message(1, "1", pending.clientId)], cursor: "1"}));
        const retry = store.send(direct, pending.text, pending);
        await vi.advanceTimersByTimeAsync(1000); await retry;
        expect(chat.mock.calls[0][0].clientId).toBe(chat.mock.calls[1][0].clientId);
        expect(store.conversation(direct).pending).toEqual([]);
        expect(store.conversation(direct).messages).toHaveLength(1);
    });
});

describe("history windows", () => {
    it("keeps a continuous old window and reloads latest instead of inserting across a gap", async () => {
        const c = store.conversation(direct);
        c.loaded = true; c.cursor = "1000"; c.messages = Array.from({length: 500}, (_, i) => message(501 + i));
        chat.mockResolvedValueOnce(page({messages: [500, 499, 498, 497].map((id) => message(id)), cursor: "497"}));
        await store.sync(direct, true);
        expect(c.olderWindow).toBe(true); expect(c.messages[0].id).toBe("497");
        expect(c.messages[c.messages.length - 1]?.id).toBe("996");
        chat.mockResolvedValueOnce(page({messages: [message(1001)], cursor: "1001", latestId: "1001"}));
        const sync = store.sync(direct); await vi.advanceTimersByTimeAsync(1000); await sync;
        expect(c.cursor).toBe("1001"); expect(c.newMessages).toBe(true);
        expect(c.messages[c.messages.length - 1]?.id).toBe("996");
        chat.mockResolvedValueOnce(page({messages: [message(1002), message(1001)], cursor: "1001", latestId: "1002"}));
        const latest = store.latest(direct); await vi.advanceTimersByTimeAsync(1000); await latest;
        expect(c.messages.map((m) => m.id)).toEqual(["1001", "1002"]);
        expect(c.olderWindow).toBe(false); expect(c.newMessages).toBe(false);
    });
    it("confirms sends in the frozen window without displaying or marking unseen messages read", async () => {
        const c = store.conversation(direct);
        c.loaded = true; c.cursor = "500"; c.messages = [message(10)]; c.olderWindow = true;
        chat.mockImplementation(async (r: ChatRequestDTO) => page({messages: [message(501, "1", r.clientId)], cursor: "501"}));
        await store.send(direct, "hello");
        expect(c.pending).toHaveLength(0); expect(c.messages.map((m) => m.id)).toEqual(["10"]);
        expect(c.newMessages).toBe(true);
        await store.markRead(direct); tick(); await vi.advanceTimersByTimeAsync(1000);
        expect(chat.mock.calls.some(([r]) => r.operation === "read")).toBe(false);
    });
});

describe("session lifecycle", () => {
    it("cancels a queued send before reconnect and preserves its original text and clientId", async () => {
        await store.sync(channel);
        const send = store.send(direct, "keep me");
        await vi.advanceTimersByTimeAsync(0);
        store.setConnected(false); store.setConnected(true);
        await vi.advanceTimersByTimeAsync(1000); await send;
        expect(chat).toHaveBeenCalledTimes(1);
        expect(store.conversation(direct).pending[0]).toMatchObject({text: "keep me", state: "error"});
    });
    it("ignores an in-flight history response from the previous session", async () => {
        let resolve!: (p: ChatPageDTO) => void;
        chat.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
        const sync = store.sync(direct); await vi.advanceTimersByTimeAsync(0);
        store.setConnected(false); store.setConnected(true);
        resolve(page({messages: [message(1)], cursor: "1"})); await sync;
        expect(store.conversation(direct).messages).toEqual([]);
        expect(store.conversation(direct).loading).toBe(false);
    });
    it("does not reopen dismissed tabs on remount or reconnect, but opens for a newer message", () => {
        const d = {userId: "2", displayName: "bob", latestId: "9", readId: "0", unread: 2};
        store.dialogs = [d]; store.dismissDialog("2");
        const unwatch = store.watch(direct); unwatch(); store.watch(direct);
        store.setConnected(false); store.setConnected(true);
        expect(store.shouldOpenDialog({...d})).toBe(false);
        expect(store.shouldOpenDialog({...d, latestId: "10"})).toBe(true);
    });
    it("does not reopen a dismissed tab for a stale catalog response", () => {
        store.conversation(direct).messages = [message(10)];
        store.dismissDialog("2");
        expect(store.shouldOpenDialog({userId: "2", displayName: "bob", latestId: "9", readId: "0", unread: 1})).toBe(false);
    });
});

describe("request scheduler", () => {
    it("coalesces notifications and duplicate watchers into one sync pass", async () => {
        store.watch(channel); store.watch(channel);
        for (let i = 0; i < 100; i++) store.invalidate();
        tick(); await vi.advanceTimersByTimeAsync(1000);
        expect(chat.mock.calls.map(([r]) => r.operation)).toEqual(["history", "dialogs"]);
        for (let i = 0; i < 100; i++) store.invalidate();
        tick(); await vi.advanceTimersByTimeAsync(0);
        expect(chat).toHaveBeenCalledTimes(2);
    });
    it("coalesces read markers to the greatest visible ID", async () => {
        store.watch(channel);
        const c = store.conversation(channel); c.loaded = true;
        for (const id of [1, 3, 2]) { c.messages = [message(id)]; c.cursor = String(id); await store.markRead(channel); }
        tick(); await vi.advanceTimersByTimeAsync(2000);
        const reads = chat.mock.calls.filter(([r]) => r.operation === "read");
        expect(reads).toHaveLength(1); expect(reads[0][0].cursor).toBe("3");
    });
    it("spaces requests by a second and pauses after a rate-limit rejection", async () => {
        chat.mockRejectedValueOnce(new Error("server error: лимит запросов чата; повторите позже"));
        await store.sync(channel);
        await store.refreshDialogs(); expect(chat).toHaveBeenCalledTimes(1);
        await vi.advanceTimersByTimeAsync(59999); await store.sync(channel);
        expect(chat).toHaveBeenCalledTimes(1);
        await vi.advanceTimersByTimeAsync(1); await store.sync(channel);
        expect(chat).toHaveBeenCalledTimes(2);
        const dialogs = store.refreshDialogs(); await vi.advanceTimersByTimeAsync(999);
        expect(chat).toHaveBeenCalledTimes(2);
        await vi.advanceTimersByTimeAsync(1); await dialogs; expect(chat).toHaveBeenCalledTimes(3);
    });
});
