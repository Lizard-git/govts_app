import {describe, expect, it} from "vitest";
import type {AdminAccount} from "../../api";
import {canBan, canEditAccount, canEditChannelLevel, canTogglePermission, levelAllowed, parseLevel, type AdminViewer} from "./adminRules";

const account = (patch: Partial<AdminAccount> = {}): AdminAccount => ({
    id: "5", name: "user", fingerprint: "00", joinLevel: 5, kick: false, ban: false, drag: false,
    admin: false, owner: false, banned: false, online: false, banReason: "", ...patch,
});
const administrator: AdminViewer = {userId: "2", isOwner: false, joinLevel: 10, canKick: true, canBan: false, canDrag: false};
const owner: AdminViewer = {userId: "1", isOwner: true, canKick: false, canBan: false, canDrag: false};

describe("parseLevel", () => {
    it("accepts 0..65535 as plain digits", () => {
        expect(parseLevel("0")).toBe(0);
        expect(parseLevel(" 25 ")).toBe(25);
        expect(parseLevel("65535")).toBe(65535);
    });
    it("rejects everything else", () => {
        for (const value of ["", "-1", "1.5", "1e3", "65536", "123456", "abc"]) expect(parseLevel(value)).toBeNull();
    });
});

describe("account rules", () => {
    it("protects the owner and the viewer's own account from everyone", () => {
        expect(canEditAccount(owner, account({owner: true, id: "9"}))).toBe(false);
        expect(canEditAccount(owner, account({id: "1"}))).toBe(false);
        expect(canEditAccount(administrator, account({id: "2"}))).toBe(false);
    });
    it("lets an administrator change only lower plain accounts", () => {
        expect(canEditAccount(administrator, account())).toBe(true);
        expect(canEditAccount(administrator, account({joinLevel: 10}))).toBe(true);
        expect(canEditAccount(administrator, account({joinLevel: 11}))).toBe(false);
        expect(canEditAccount(administrator, account({admin: true}))).toBe(false);
        expect(canEditAccount(owner, account({admin: true, joinLevel: 60000}))).toBe(true);
    });
    it("grants only held permissions and never admin unless owner", () => {
        expect(canTogglePermission(administrator, account(), "kick")).toBe(true);
        expect(canTogglePermission(administrator, account(), "ban")).toBe(false);
        expect(canTogglePermission(administrator, account(), "admin")).toBe(false);
        expect(canTogglePermission(owner, account(), "admin")).toBe(true);
        expect(canTogglePermission(administrator, account({joinLevel: 11}), "kick")).toBe(false);
    });
    it("needs the ban permission to ban unless owner", () => {
        expect(canBan(administrator, account())).toBe(false);
        expect(canBan({...administrator, canBan: true}, account())).toBe(true);
        expect(canBan(owner, account())).toBe(true);
    });
});

describe("level rules", () => {
    it("caps levels at the viewer's own unless owner", () => {
        expect(levelAllowed(administrator, 10)).toBe(true);
        expect(levelAllowed(administrator, 11)).toBe(false);
        expect(levelAllowed(administrator, null)).toBe(false);
        expect(levelAllowed(owner, 65535)).toBe(true);
        expect(levelAllowed({...administrator, joinLevel: undefined}, 1)).toBe(false);
    });
    it("locks channels above the viewer's level", () => {
        expect(canEditChannelLevel(administrator, 10)).toBe(true);
        expect(canEditChannelLevel(administrator, 30)).toBe(false);
        expect(canEditChannelLevel(owner, 30)).toBe(true);
    });
});
