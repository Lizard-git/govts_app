import {useEffect, useRef, useState, type ReactNode} from "react";

export type SelectOption = {
    value: string;
    label: string;
    detail?: string;
    selectedLabel?: string;
};

export function Select({id, label, value, options, disabled = false, onChange, hint, labelExtra, className}: {
    id: string;
    label: string;
    value: string;
    options: SelectOption[];
    disabled?: boolean;
    onChange: (value: string) => void;
    hint?: string;
    labelExtra?: ReactNode;
    className?: string;
}) {
    const [open, setOpen] = useState(false);
    const rootRef = useRef<HTMLDivElement>(null);
    const labelId = `${id}-label`;
    const selected = options.find((option) => option.value === value);
    const selectedName = selected?.selectedLabel ?? selected?.label ?? value;

    useEffect(() => {
        if (disabled) setOpen(false);
    }, [disabled]);

    useEffect(() => {
        if (!open) return;
        const closeOnEscape = (event: KeyboardEvent) => {
            if (event.key !== "Escape") return;
            event.preventDefault();
            event.stopPropagation();
            setOpen(false);
        };
        const closeOnPointer = (event: PointerEvent) => {
            if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
        };
        window.addEventListener("keydown", closeOnEscape, true);
        window.addEventListener("pointerdown", closeOnPointer);
        return () => {
            window.removeEventListener("keydown", closeOnEscape, true);
            window.removeEventListener("pointerdown", closeOnPointer);
        };
    }, [open]);

    const choose = (next: string) => {
        setOpen(false);
        if (next !== value) onChange(next);
    };

    return <div className={className ? `app-select ${className}` : "app-select"} ref={rootRef}>
        {labelExtra
            ? <div className="app-select-heading"><span className="setting-label" id={labelId}>{label}</span>{labelExtra}</div>
            : <span className="setting-label" id={labelId}>{label}</span>}
        <div className="app-select-control">
            <button id={id} className="app-select-trigger" type="button" disabled={disabled}
                    aria-labelledby={`${labelId} ${id}`} aria-haspopup="listbox" aria-expanded={open}
                    onClick={() => setOpen((current) => !current)}><span>{selectedName}</span></button>
            {open && <div className="app-select-options" role="listbox" aria-labelledby={labelId}>
                {options.map((option) => <button type="button" role="option" aria-selected={option.value === value}
                                                  className={option.value === value ? "selected" : ""}
                                                  onClick={() => choose(option.value)}
                                                  key={option.value}>{option.label}{option.detail ?
                    <small>{option.detail}</small> : null}</button>)}
            </div>}
        </div>
        {hint && <small>{hint}</small>}
    </div>;
}
