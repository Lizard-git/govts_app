import {useEffect, useRef, useState} from "react";
import {createPortal} from "react-dom";
import type {ParticipantDTO} from "../../api";
import {desktopAPI} from "../../api";

type MenuState = {x: number; y: number; volume: number} | null;

export function ParticipantRow({participant, depth, onError}: {
    participant: ParticipantDTO;
    depth: number;
    onError: (message: string) => void;
}) {
    const [menu, setMenu] = useState<MenuState>(null);
    const rowRef = useRef<HTMLDivElement>(null);
    const menuRef = useRef<HTMLDivElement>(null);
    const timerRef = useRef<number | undefined>(undefined);
    const errorText = (error: unknown) => (error instanceof Error ? error.message : String(error)).replace(/^Error:\s*/, "");

    const open = async (x: number, y: number) => {
        if (participant.local) return;
        try {
            const volume = await desktopAPI.participantVolume(participant.sessionId);
            setMenu({x, y, volume});
        } catch (error) {
            onError(errorText(error));
        }
    };
    const close = () => {
        setMenu(null);
        rowRef.current?.focus();
    };
    const setVolume = (volume: number, immediate = false) => {
        setMenu((current) => current ? {...current, volume} : current);
        window.clearTimeout(timerRef.current);
        const commit = () => void desktopAPI.setParticipantVolume(participant.sessionId, volume).catch((error) => onError(errorText(error)));
        if (immediate) commit();
        else timerRef.current = window.setTimeout(commit, 40);
    };

    useEffect(() => () => window.clearTimeout(timerRef.current), []);
    useEffect(() => {
        if (!menu) return;
        const dismiss = (event: PointerEvent) => {
            if (!menuRef.current?.contains(event.target as Node)) close();
        };
        const keydown = (event: KeyboardEvent) => {
            if (event.key === "Escape") close();
        };
        window.addEventListener("pointerdown", dismiss);
        window.addEventListener("keydown", keydown);
        requestAnimationFrame(() => menuRef.current?.querySelector<HTMLInputElement>("input")?.focus());
        return () => {
            window.removeEventListener("pointerdown", dismiss);
            window.removeEventListener("keydown", keydown);
        };
    }, [menu?.x, menu?.y]);

    const position = menu ? {
        left: Math.max(8, Math.min(menu.x, window.innerWidth - 284)),
        top: Math.max(8, Math.min(menu.y, window.innerHeight - 158)),
    } : undefined;
    return <>
        <div ref={rowRef} className={`participant-row ${participant.speaking ? "speaking" : ""}`}
             style={{paddingLeft: 46 + depth * 18}} tabIndex={participant.local ? -1 : 0}
             onContextMenu={(event) => {
                 if (participant.local) return;
                 event.preventDefault();
                 void open(event.clientX, event.clientY);
             }} onKeyDown={(event) => {
                if (!participant.local && (event.key === "ContextMenu" || (event.shiftKey && event.key === "F10"))) {
                    event.preventDefault();
                    const bounds = event.currentTarget.getBoundingClientRect();
                    void open(bounds.left + 40, bounds.bottom);
                }
             }}><span className="avatar">{participant.displayName.slice(0, 1).toUpperCase()}</span>
            <span>{participant.displayName}{participant.local ? " (вы)" : ""}</span>
            <span className="speaking-ring" aria-label={participant.speaking ? "Говорит" : "Не говорит"}/>
        </div>
        {menu && createPortal(<div ref={menuRef} className="participant-menu" role="menu" style={position}
                                   aria-label={`Управление пользователем ${participant.displayName}`}>
            <div className="participant-menu-user">
                <span className="participant-menu-avatar" aria-hidden="true">{participant.displayName.slice(0, 1).toUpperCase()}</span>
                <strong>{participant.displayName}</strong>
            </div>
            <div className="participant-menu-separator"/>
            <div className="participant-volume-heading">
                <label htmlFor={`participant-volume-${participant.sessionId}`}>Громкость пользователя</label>
                <output>{Math.round(menu.volume * 100)}%</output>
            </div>
            <div className="participant-volume-control">
                <span className="participant-volume-icon" aria-hidden="true">◖</span>
                <input id={`participant-volume-${participant.sessionId}`} type="range" min="0" max="2" step="0.01"
                       style={{"--participant-volume": `${menu.volume / 2 * 100}%`} as React.CSSProperties}
                       value={menu.volume} onChange={(event) => setVolume(Number(event.target.value))}
                       onPointerUp={(event) => setVolume(Number(event.currentTarget.value), true)}
                       onKeyUp={(event) => setVolume(Number(event.currentTarget.value), true)}/>
            </div>
            <div className="participant-menu-actions">
                <button type="button" role="menuitem" className={menu.volume === 0 ? "active" : ""}
                        onClick={() => setVolume(menu.volume === 0 ? 1 : 0, true)}>
                    <span aria-hidden="true">{menu.volume === 0 ? "🔊" : "🔇"}</span>{menu.volume === 0 ? "Включить звук" : "Заглушить"}
                </button>
            </div>
        </div>, document.body)}
    </>;
}
