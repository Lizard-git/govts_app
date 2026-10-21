import {useCallback, useEffect, useMemo, useState} from "react";
import {desktopAPI, type AdminAccount, type AdminAuditEntry, type AdminPermission, type ClientViewDTO} from "../../api";
import {errorText} from "../../locales";
import {useLocale} from "../../locales/Locale";
import {canBan, canEditAccount, canEditChannelLevel, canTogglePermission, levelAllowed, parseLevel} from "./adminRules";

type Section = "accounts" | "channels" | "audit";

const maxReasonBytes = 128;

export function AdminPanel({view}: {view: ClientViewDTO}) {
    const {m} = useLocale();
    const [section, setSection] = useState<Section>("accounts");
    return <section className="settings-page admin-page">
        <div className="settings-heading"><p className="eyebrow">{view.server.name || m.connection.server}</p><h2>{m.admin.title}</h2></div>
        <div className="settings-layout">
            <nav className="settings-nav" aria-label={m.admin.sections}>
                <button type="button" aria-current={section === "accounts" ? "page" : undefined} onClick={() => setSection("accounts")}>{m.admin.accounts}</button>
                <button type="button" aria-current={section === "channels" ? "page" : undefined} onClick={() => setSection("channels")}>{m.admin.channels}</button>
                <button type="button" aria-current={section === "audit" ? "page" : undefined} onClick={() => setSection("audit")}>{m.admin.audit}</button>
            </nav>
            <div className="settings-content">
                {section === "accounts" && <AccountsSection view={view}/>}
                {section === "channels" && <ChannelsSection view={view}/>}
                {section === "audit" && <AuditSection/>}
            </div>
        </div>
    </section>;
}

function useStatus() {
    const {m} = useLocale();
    const [error, setError] = useState("");
    const [busy, setBusy] = useState(false);
    const run = useCallback(async (operation: () => Promise<unknown>) => {
        setBusy(true);
        setError("");
        try {
            await operation();
            return true;
        } catch (cause) {
            setError(errorText(cause, m));
            return false;
        } finally {
            setBusy(false);
        }
    }, [m]);
    return {error, busy, run};
}

function AccountsSection({view}: {view: ClientViewDTO}) {
    const {m} = useLocale();
    const [accounts, setAccounts] = useState<AdminAccount[]>([]);
    const [next, setNext] = useState("");
    const [loaded, setLoaded] = useState(false);
    const [query, setQuery] = useState("");
    const [selectedID, setSelectedID] = useState("");
    const {error, busy, run} = useStatus();

    const load = useCallback((cursor: string) => run(async () => {
        const page = await desktopAPI.adminAccounts(cursor);
        const items = page.accounts ?? [];
        setAccounts((current) => cursor === "" ? items : [...current, ...items.filter((item) => !current.some((known) => known.id === item.id))]);
        setNext(page.next);
        setLoaded(true);
    }), [run]);
    useEffect(() => { void load(""); }, [load]);

    const replace = (account: AdminAccount) => setAccounts((current) => current.map((item) => item.id === account.id ? account : item));
    const filter = query.trim().toLowerCase();
    const visible = useMemo(() => filter === "" ? accounts : accounts.filter((account) =>
        account.name.toLowerCase().includes(filter) || account.id === filter || account.fingerprint.startsWith(filter)), [accounts, filter]);
    const selected = accounts.find((account) => account.id === selectedID);

    return <div className="admin-accounts">
        <section className="settings-card admin-list-card">
            <h3>{m.admin.accounts}</h3>
            <input type="search" className="admin-search" value={query} placeholder={m.admin.search} aria-label={m.admin.search}
                   onChange={(event) => setQuery(event.target.value)}/>
            {error && <div className="device-error" role="alert">{error}</div>}
            <ul className="admin-list" aria-busy={busy}>
                {visible.map((account) => <li key={account.id}>
                    <button type="button" className="admin-account-row" aria-pressed={account.id === selectedID} onClick={() => setSelectedID(account.id)}>
                        <span className={`admin-presence ${account.online ? "online" : ""}`} aria-hidden="true"/>
                        <span className="admin-account-name">{account.name}</span>
                        <span className="admin-account-id">#{account.id}</span>
                        <AccountBadges account={account}/>
                    </button>
                </li>)}
            </ul>
            {loaded && visible.length === 0 && <p className="device-hint">{m.admin.empty}</p>}
            <div className="admin-list-actions">
                {next && <button type="button" className="secondary-button" disabled={busy} onClick={() => void load(next)}>{busy ? m.admin.loading : m.admin.loadMore}</button>}
                <button type="button" className="text-button" disabled={busy} onClick={() => void load("")}>{m.admin.reload}</button>
            </div>
        </section>
        {selected ? <AccountCard key={selected.id} view={view} account={selected} onChange={replace}/>
            : <section className="settings-card admin-detail-card"><p className="device-hint">{m.admin.selectAccount}</p></section>}
    </div>;
}

function AccountBadges({account}: {account: AdminAccount}) {
    const {m} = useLocale();
    return <span className="admin-badges">
        {account.owner && <span className="admin-badge accent">{m.admin.owner}</span>}
        {account.admin && <span className="admin-badge">{m.admin.admin}</span>}
        {account.banned && <span className="admin-badge danger">{m.admin.banned}</span>}
        {account.online && <span className="admin-badge">{m.admin.online}</span>}
    </span>;
}

function AccountCard({view, account, onChange}: {view: ClientViewDTO; account: AdminAccount; onChange: (account: AdminAccount) => void}) {
    const {m} = useLocale();
    const {error, busy, run} = useStatus();
    const [level, setLevel] = useState(String(account.joinLevel));
    const [reason, setReason] = useState("");
    const editable = canEditAccount(view, account);
    const banAllowed = canBan(view, account);
    const parsedLevel = parseLevel(level);
    const levelValid = levelAllowed(view, parsedLevel);
    const reasonTooLong = new TextEncoder().encode(reason).length > maxReasonBytes;

    const update = (operation: () => Promise<AdminAccount>) => run(async () => {
        const changed = await operation();
        onChange(changed);
        setLevel(String(changed.joinLevel));
    });
    const permissions: {id: AdminPermission; title: string; hint: string}[] = [
        {id: "kick", title: m.admin.permKick, hint: m.admin.permKickHint},
        {id: "ban", title: m.admin.permBan, hint: m.admin.permBanHint},
        {id: "drag", title: m.admin.permDrag, hint: m.admin.permDragHint},
        {id: "admin", title: m.admin.permAdmin, hint: m.admin.permAdminHint},
    ];

    return <section className="settings-card admin-detail-card" aria-busy={busy}>
        <h3>{account.name} <span className="admin-account-id">#{account.id}</span></h3>
        <p className="device-hint admin-fingerprint">{m.admin.fingerprint}: <code>{account.fingerprint}</code></p>
        <AccountBadges account={account}/>
        {!editable && <p className="device-hint">{m.admin.protected}</p>}
        {error && <div className="device-error" role="alert">{error}</div>}

        {permissions.map((permission) => <div key={permission.id} className="setting-toggle">
            <div><strong>{permission.title}</strong><p>{permission.hint}</p></div>
            <label className="switch"><input type="checkbox" checked={account[permission.id]} disabled={busy || !canTogglePermission(view, account, permission.id)}
                                             onChange={(event) => void update(() => desktopAPI.adminSetPermission(account.id, permission.id, event.target.checked))}/><span/></label>
        </div>)}

        <form className="admin-inline-form" onSubmit={(event) => {
            event.preventDefault();
            if (parsedLevel !== null && levelValid) void update(() => desktopAPI.adminSetJoinLevel(account.id, parsedLevel));
        }}>
            <label><span className="setting-label">{m.admin.joinLevel}</span>
                <input inputMode="numeric" value={level} disabled={busy || !editable} onChange={(event) => setLevel(event.target.value)}/></label>
            <button type="submit" className="secondary-button" disabled={busy || !editable || !levelValid || parsedLevel === account.joinLevel}>{m.admin.save}</button>
            <p className="device-hint">{view.isOwner ? m.admin.joinLevelHint : m.admin.joinLevelLimit((view.joinLevel ?? 0))}</p>
        </form>

        <div className="admin-inline-form admin-ban">
            {account.banned ? <>
                <p className="device-hint">{m.admin.bannedReason(account.banReason)}</p>
                <button type="button" className="secondary-button" disabled={busy || !banAllowed}
                        onClick={() => void update(() => desktopAPI.adminSetBan(account.id, false, ""))}>{m.admin.unban}</button>
            </> : <>
                <label><span className="setting-label">{m.admin.banReason}</span>
                    <input value={reason} maxLength={maxReasonBytes} disabled={busy || !editable} onChange={(event) => setReason(event.target.value)}/></label>
                <button type="button" className="secondary-button danger-button" disabled={busy || !banAllowed || reasonTooLong}
                        onClick={() => {
                            if (!window.confirm(m.admin.banConfirm(account.name))) return;
                            void update(() => desktopAPI.adminSetBan(account.id, true, reason.trim()));
                        }}>{m.admin.ban}</button>
            </>}
        </div>
    </section>;
}

function ChannelsSection({view}: {view: ClientViewDTO}) {
    const {m} = useLocale();
    const channels = view.channels ?? [];
    return <section className="settings-card">
        <h3>{m.admin.channels}</h3>
        <p className="device-hint">{m.admin.channelsHint}</p>
        {channels.map((channel) => <ChannelLevelRow key={channel.id} view={view} channelID={channel.id} name={channel.name} level={channel.minJoinLevel ?? 0}/>)}
    </section>;
}

function ChannelLevelRow({view, channelID, name, level}: {view: ClientViewDTO; channelID: string; name: string; level: number}) {
    const {m} = useLocale();
    const {error, busy, run} = useStatus();
    const [value, setValue] = useState(String(level));
    useEffect(() => setValue(String(level)), [level]);
    const parsed = parseLevel(value);
    const editable = canEditChannelLevel(view, level);
    const valid = levelAllowed(view, parsed);
    return <form className="admin-inline-form admin-channel-row" onSubmit={(event) => {
        event.preventDefault();
        if (parsed !== null && valid) void run(() => desktopAPI.adminSetChannelJoinLevel(channelID, parsed));
    }}>
        <label><span className="setting-label">{name}</span>
            <input inputMode="numeric" aria-label={m.admin.minJoinLevel(name)} value={value} disabled={busy || !editable} onChange={(event) => setValue(event.target.value)}/></label>
        <button type="submit" className="secondary-button" disabled={busy || !editable || !valid || parsed === level}>{m.admin.save}</button>
        {error && <div className="device-error" role="alert">{error}</div>}
    </form>;
}

function AuditSection() {
    const {m, htmlLang} = useLocale();
    const [entries, setEntries] = useState<AdminAuditEntry[]>([]);
    const [next, setNext] = useState("");
    const [loaded, setLoaded] = useState(false);
    const {error, busy, run} = useStatus();
    const load = useCallback((cursor: string) => run(async () => {
        const page = await desktopAPI.adminAudit(cursor);
        const items = page.entries ?? [];
        setEntries((current) => cursor === "" ? items : [...current, ...items.filter((item) => !current.some((known) => known.id === item.id))]);
        setNext(page.next);
        setLoaded(true);
    }), [run]);
    useEffect(() => { void load(""); }, [load]);
    const format = useMemo(() => new Intl.DateTimeFormat(htmlLang, {dateStyle: "short", timeStyle: "medium"}), [htmlLang]);
    const actorName = (actor: string) => actor === "local-console" ? m.admin.console : m.admin.accountRef(actor);
    return <section className="settings-card">
        <h3>{m.admin.audit}</h3>
        {error && <div className="device-error" role="alert">{error}</div>}
        <table className="admin-audit" aria-busy={busy}>
            <thead><tr><th>{m.admin.auditTime}</th><th>{m.admin.auditActor}</th><th>{m.admin.auditAction}</th><th>{m.admin.auditTarget}</th><th>{m.admin.auditDetail}</th></tr></thead>
            <tbody>
                {entries.map((entry) => <tr key={entry.id}>
                    <td>{entry.happenedAtMs > 0 ? format.format(new Date(entry.happenedAtMs)) : "—"}</td>
                    <td>{actorName(entry.actor)}</td>
                    <td><code>{entry.action}</code></td>
                    <td>{entry.targetUserId ? m.admin.accountRef(entry.targetUserId) : "—"}</td>
                    <td className="admin-audit-detail">{entry.detail}</td>
                </tr>)}
            </tbody>
        </table>
        {loaded && entries.length === 0 && <p className="device-hint">{m.admin.auditEmpty}</p>}
        <div className="admin-list-actions">
            {next && <button type="button" className="secondary-button" disabled={busy} onClick={() => void load(next)}>{busy ? m.admin.loading : m.admin.loadMore}</button>}
            <button type="button" className="text-button" disabled={busy} onClick={() => void load("")}>{m.admin.reload}</button>
        </div>
    </section>;
}
