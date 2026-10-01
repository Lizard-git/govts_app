import {useLayoutEffect, useRef, useState} from "react";
import type {CSSProperties, KeyboardEvent, PointerEvent} from "react";

type Side = "browser" | "info";
const defaults = {browser: 210, info: 245};
const dividerWidth = 6;
const minimumCenterWidth = 520;

export function useWorkspaceResize(showInfo: boolean) {
    const layoutRef = useRef<HTMLDivElement>(null);
    const drag = useRef<{side: Side; startX: number; startWidth: number} | null>(null);
    const [resizing, setResizing] = useState(false);
    const [availableWidth, setAvailableWidth] = useState(window.innerWidth);
    const [preferredWidths, setPreferredWidths] = useState(defaults);

    useLayoutEffect(() => {
        const element = layoutRef.current;
        if (!element) return;
        setAvailableWidth(element.clientWidth);
        const observer = new ResizeObserver(() => setAvailableWidth(element.clientWidth));
        observer.observe(element);
        return () => observer.disconnect();
    }, []);

    const overlay = availableWidth <= 850;
    const browserMin = Math.min(160, availableWidth / 3);
    const infoMin = Math.min(180, availableWidth / 3);
    const centerMin = Math.min(minimumCenterWidth, Math.max(0, availableWidth - browserMin - dividerWidth -
        (showInfo && !overlay ? infoMin + dividerWidth : 0)));
    const infoWidth = Math.max(infoMin, Math.min(preferredWidths.info,
        availableWidth - centerMin - (overlay ? dividerWidth : browserMin + 2 * dividerWidth)));
    const browserMax = Math.max(browserMin, availableWidth - centerMin - dividerWidth -
        (showInfo && !overlay ? infoWidth + dividerWidth : 0));
    const browserWidth = Math.max(browserMin, Math.min(preferredWidths.browser, browserMax));
    const infoMax = Math.max(infoMin, availableWidth - centerMin -
        (overlay ? dividerWidth : browserWidth + 2 * dividerWidth));

    const widths = {browser: browserWidth, info: infoWidth};
    const minimums = {browser: browserMin, info: infoMin};
    const maximums = {browser: browserMax, info: infoMax};
    const resize = (side: Side, value: number) => {
        const next = Math.max(minimums[side], Math.min(value, maximums[side]));
        setPreferredWidths((current) => ({...current, [side]: next}));
    };
    const stop = () => { drag.current = null; setResizing(false); };
    const separatorProps = (side: Side) => ({
        role: "separator" as const,
        tabIndex: 0,
        "aria-orientation": "vertical" as const,
        "aria-label": side === "browser" ? "Ширина списка каналов" : "Ширина информации о канале",
        "aria-controls": side === "browser" ? "channel-browser-pane" : "channel-info-pane",
        "aria-valuemin": Math.round(minimums[side]),
        "aria-valuemax": Math.round(maximums[side]),
        "aria-valuenow": Math.round(widths[side]),
        "aria-valuetext": `${Math.round(widths[side])} пикселей`,
        onPointerDown: (event: PointerEvent<HTMLDivElement>) => {
            if (event.button !== 0) return;
            event.preventDefault();
            event.currentTarget.focus();
            event.currentTarget.setPointerCapture(event.pointerId);
            drag.current = {side, startX: event.clientX, startWidth: widths[side]};
            setResizing(true);
        },
        onPointerMove: (event: PointerEvent<HTMLDivElement>) => {
            const current = drag.current;
            if (!current || current.side !== side) return;
            resize(side, current.startWidth + (event.clientX - current.startX) * (side === "browser" ? 1 : -1));
        },
        onPointerUp: (event: PointerEvent<HTMLDivElement>) => {
            if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
            stop();
        },
        onPointerCancel: stop,
        onLostPointerCapture: stop,
        onDoubleClick: () => resize(side, defaults[side]),
        onKeyDown: (event: KeyboardEvent<HTMLDivElement>) => {
            if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
            event.preventDefault();
            const direction = (event.key === "ArrowRight" ? 1 : -1) * (side === "browser" ? 1 : -1);
            resize(side, widths[side] + direction * (event.shiftKey ? 30 : 10));
        },
    });

    return {layoutRef, resizing, separatorProps, style: {
        "--channel-browser-width": `${browserWidth}px`,
        "--channel-info-width": `${infoWidth}px`,
    } as CSSProperties};
}
