import type {AdminAccount, AdminPermission} from "../../api";

// The part of ClientViewDTO the administration rules read.
export type AdminViewer = {userId: string; isOwner: boolean; joinLevel?: number; canKick: boolean; canBan: boolean; canDrag: boolean};

export const maxLevel = 65535;

export function parseLevel(value: string): number | null {
    if (!/^\d{1,5}$/.test(value.trim())) return null;
    const level = Number(value);
    return level <= maxLevel ? level : null;
}

// These rules mirror the server hierarchy (internal/voice/admin.go) so that
// controls the server would refuse are disabled; the server still decides.
export function canEditAccount(viewer: AdminViewer, account: AdminAccount): boolean {
    if (account.owner || account.id === viewer.userId) return false;
    return viewer.isOwner || (!account.admin && account.joinLevel <= (viewer.joinLevel ?? 0));
}

export function canTogglePermission(viewer: AdminViewer, account: AdminAccount, permission: AdminPermission): boolean {
    if (!canEditAccount(viewer, account)) return false;
    if (viewer.isOwner) return true;
    const held: Record<AdminPermission, boolean> = {kick: viewer.canKick, ban: viewer.canBan, drag: viewer.canDrag, admin: false};
    return held[permission];
}

export function canBan(viewer: AdminViewer, account: AdminAccount): boolean {
    return canEditAccount(viewer, account) && (viewer.isOwner || viewer.canBan);
}

export function levelAllowed(viewer: AdminViewer, level: number | null): boolean {
    return level !== null && (viewer.isOwner || level <= (viewer.joinLevel ?? 0));
}

export function canEditChannelLevel(viewer: AdminViewer, current: number): boolean {
    return viewer.isOwner || current <= (viewer.joinLevel ?? 0);
}
