import {useEffect, useRef, useState} from "react";
import {
    builtinThemes,
    cleanThemeName,
    fontFamilies,
    fontSizes,
    fontStack,
    readStageImage,
    stageBackdropOptions,
    stageBackdrops,
    stageImageBackground,
    themePalettes,
    type Appearance,
    type FontFamily,
    type PaletteToken,
    type SavedTheme,
    type StageBackdrop,
} from "../../appearance";
import {errorCode, errorText} from "../../locales";
import {useLocale} from "../../locales/Locale";
import {StageScene} from "./StageScene";

const paletteTokens: {title: "surfaces" | "text" | "accents"; fields: PaletteToken[]}[] = [
    {title: "surfaces", fields: ["window", "card", "cardStrong", "line"]},
    {title: "text", fields: ["text", "muted", "onAccent"]},
    {title: "accents", fields: ["accent", "accentBlue", "danger", "warning"]},
];

export function InterfaceSettings({appearance, onTheme, onCreate, onChange, onOpenSaved, onSave, onImport, onExport, onDelete}: {
    appearance: Appearance;
    onTheme: (theme: string) => Promise<void>;
    onCreate: () => void;
    onChange: (patch: Partial<Appearance>) => void;
    onOpenSaved: (id: string) => void;
    onSave: (name: string) => void;
    onImport: (file: File) => Promise<void>;
    onExport: (theme: SavedTheme) => void;
    onDelete: (id: string) => void;
}) {
    const {m} = useLocale();
    const imageRef = useRef<HTMLInputElement>(null);
    const importRef = useRef<HTMLInputElement>(null);
    const [notice, setNotice] = useState("");
    const [name, setName] = useState(appearance.customName);
    const custom = appearance.theme === "custom";
    useEffect(() => { setName(appearance.customName); }, [appearance.customName, appearance.activeSavedId, appearance.theme]);

    const commit = (patch: Partial<Appearance>) => {
        try {
            onChange(patch);
            setNotice("");
        } catch (error) {
            setNotice(error instanceof Error ? error.message : errorCode("theme-save-failed"));
        }
    };

    const chooseImage = async (file: File | undefined) => {
        if (!file) return;
        try {
            const stageImage = await readStageImage(file);
            commit({stageBackdrop: "image", stageImage});
        } catch (error) {
            setNotice(error instanceof Error ? error.message : errorCode("image-read"));
        } finally {
            if (imageRef.current) imageRef.current.value = "";
        }
    };

    const importFile = async (file: File | undefined) => {
        if (!file) return;
        try {
            await onImport(file);
            setNotice("");
        } catch (error) {
            setNotice(error instanceof Error ? error.message : errorCode("theme-import-failed"));
        } finally {
            if (importRef.current) importRef.current.value = "";
        }
    };

    const save = () => {
        try {
            onSave(name);
            setNotice("");
        } catch (error) {
            setNotice(error instanceof Error ? error.message : errorCode("theme-save-failed"));
        }
    };

    return <>
        <div className="choice-heading">{m.theme.heading}</div>
        <div className="theme-gallery" role="group" aria-label={m.theme.gallery}>
            {builtinThemes.map((theme) => <ThemeSwatch key={theme} label={m.theme.names[theme]} palette={themePalettes[theme]}
                                                        fontFamily="Noto Sans" pressed={appearance.theme === theme}
                                                        onClick={() => void onTheme(theme)}/>)}
            {appearance.savedThemes.map((theme) => <ThemeSwatch key={theme.id} label={theme.name} palette={theme.palette}
                                                                 fontFamily={theme.fontFamily}
                                                                 pressed={custom && appearance.activeSavedId === theme.id}
                                                                 onClick={() => onOpenSaved(theme.id)}
                                                                 onExport={() => onExport(theme)}
                                                                 onDelete={() => onDelete(theme.id)}/>)}
            {appearance.hasDraft && <ThemeSwatch label={appearance.customName || m.theme.draft} palette={appearance.custom}
                                                  fontFamily={appearance.fontFamily}
                                                  pressed={custom && !appearance.activeSavedId}
                                                  onClick={() => void onTheme("custom")}/>}
        </div>
        <p className="theme-note">{m.theme.builtinNote}</p>
        <div className="theme-actions">
            <button type="button" className="primary-button theme-create" onClick={onCreate}>{m.theme.create}</button>
            <button type="button" className="secondary-button" onClick={() => importRef.current?.click()}>{m.theme.import}</button>
            <input ref={importRef} type="file" accept="application/json,.json" hidden
                   onChange={(event) => void importFile(event.currentTarget.files?.[0])}/>
        </div>
        {appearance.savedThemes.length > 0 && <p className="theme-note">{m.theme.copyNote}</p>}
        {custom && <div className="theme-editor">
            <div className="theme-save">
                <label className="setting-label" htmlFor="theme-name">{m.theme.name}</label>
                <input id="theme-name" className="theme-name" maxLength={40} value={name} placeholder={m.theme.customName}
                       onChange={(event) => setName(event.target.value)}/>
                <div className="theme-actions">
                    <button type="button" className="primary-button theme-create" onClick={save}>{appearance.activeSavedId ? m.theme.save : m.theme.saveToList}</button>
                    <button type="button" className="secondary-button" onClick={() => onExport({
                        id: appearance.activeSavedId || "draft",
                        name: cleanThemeName(name, m.theme.customName),
                        palette: appearance.custom,
                        fontFamily: appearance.fontFamily,
                        fontSize: appearance.fontSize,
                        stageBackdrop: appearance.stageBackdrop,
                        stageImage: appearance.stageBackdrop === "image" ? appearance.stageImage : "",
                    })}>{m.theme.export}</button>
                </div>
            </div>
            <fieldset className="palette-editor">
                <legend>{m.theme.colors}</legend>
                {paletteTokens.map((group) => <fieldset className="palette-group" key={group.title}>
                    <legend>{m.theme.groups[group.title]}</legend>
                    <div className="palette-grid">
                        {group.fields.map((token) => {
                            const color = appearance.custom[token];
                            const label = m.theme.tokens[token];
                            return <label className="palette-field" key={token}>
                                <input type="color" aria-label={label} value={color}
                                       onInput={(event) => commit({custom: {...appearance.custom, [token]: event.currentTarget.value}})}/>
                                <span>{label}</span>
                                <code>{color}</code>
                            </label>;
                        })}
                    </div>
                </fieldset>)}
            </fieldset>
            <div>
                <div className="choice-heading" id="font-family-label">{m.theme.font}</div>
                <div className="font-grid" role="group" aria-labelledby="font-family-label">
                    {fontFamilies.map((font) => <button type="button" className="font-option" key={font}
                                                         aria-pressed={appearance.fontFamily === font}
                                                         style={{fontFamily: fontStack(font)}}
                                                         onClick={() => commit({fontFamily: font})}>
                        <strong>{font}</strong>
                        <span>{m.theme.sample}</span>
                    </button>)}
                </div>
            </div>
            <div>
                <div className="choice-heading" id="font-size-label">{m.theme.fontSize}</div>
                <div className="size-row" role="group" aria-labelledby="font-size-label">
                    {fontSizes.map((size) => <button type="button" className="size-chip" key={size} aria-pressed={appearance.fontSize === size}
                                                      style={{fontSize: size}}
                                                      onClick={() => commit({fontSize: size})}>{size}</button>)}
                </div>
                <p className="theme-note">{m.theme.sizeNote}</p>
            </div>
            <div>
                <div className="choice-heading" id="backdrop-label">{m.theme.backdrop}</div>
                <div className="backdrop-grid" role="group" aria-labelledby="backdrop-label">
                    {stageBackdropOptions.map((option) => <button type="button" className="backdrop-option" key={option.value}
                                                                    aria-pressed={appearance.stageBackdrop === option.value}
                                                                    onClick={() => {
                                                                        if (!stageBackdrops.some((backdrop) => backdrop === option.value)) return;
                                                                        setNotice("");
                                                                        commit({stageBackdrop: option.value as StageBackdrop});
                                                                    }}>
                        <span className="backdrop-preview" data-backdrop={option.value}
                              style={option.value === "image" && stageImageBackground(appearance.stageImage) ? {backgroundImage: stageImageBackground(appearance.stageImage) ?? undefined} : undefined}>
                            <StageScene backdrop={option.value}/>
                            <i className="backdrop-avatar"/>
                        </span>
                        <span>{m.theme.backdrops[option.value]}</span>
                    </button>)}
                </div>
            </div>
            {appearance.stageBackdrop === "image" && <div className="stage-image-picker">
                <button type="button" className="secondary-button" onClick={() => imageRef.current?.click()}>{m.theme.chooseImage}</button>
                <input ref={imageRef} type="file" accept="image/png,image/jpeg,image/webp" hidden
                       onChange={(event) => void chooseImage(event.currentTarget.files?.[0])}/>
                {stageImageBackground(appearance.stageImage) && <img className="stage-image-preview" alt="" src={appearance.stageImage}/>}
                <p className="theme-note">{m.theme.imageNote}</p>
            </div>}
            {notice && <div className="form-error" role="alert">{errorText(notice, m)}</div>}
        </div>}
        {!custom && notice && <div className="form-error" role="alert">{errorText(notice, m)}</div>}
    </>;
}

function ThemeSwatch({label, palette, fontFamily, pressed, onClick, onExport, onDelete}: {
    label: string;
    palette: Appearance["custom"];
    fontFamily: FontFamily;
    pressed: boolean;
    onClick: () => void;
    onExport?: () => void;
    onDelete?: () => void;
}) {
    const {m} = useLocale();
    return <div className="theme-swatch">
        <button type="button" className="theme-swatch-open" aria-pressed={pressed} onClick={onClick}>
            <span className="theme-swatch-canvas" style={{background: palette.window}}>
                <span className="theme-swatch-card" style={{background: palette.card, borderColor: palette.line, color: palette.text, fontFamily: fontStack(fontFamily)}}>
                    <i style={{background: palette.accent}}/>
                    <b>{m.theme.swatch}</b>
                </span>
            </span>
            <span className="theme-swatch-label">{label}</span>
        </button>
        {(onExport || onDelete) && <div className="theme-swatch-actions">
            {onExport && <button type="button" className="text-button" onClick={onExport}>{m.theme.export}</button>}
            {onDelete && <button type="button" className="text-button" onClick={onDelete}>{m.theme.remove}</button>}
        </div>}
    </div>;
}
