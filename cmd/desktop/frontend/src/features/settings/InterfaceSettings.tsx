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
    type BuiltinTheme,
    type FontFamily,
    type PaletteToken,
    type SavedTheme,
    type StageBackdrop,
} from "../../appearance";
import {StageScene} from "./StageScene";

const builtinLabels: Record<BuiltinTheme, string> = {
    light: "Светлая",
    neutral: "Темная",
    dark: "Темно-синяя",
    beige: "Бежевая",
    brown: "Коричневая",
    purple: "Фиолетовая",
};

const fontChoices: {value: FontFamily; detail: string}[] = [
    {value: "Noto Sans", detail: "Нейтральный гротеск"},
    {value: "Golos Text", detail: "Русский гротеск"},
    {value: "Manrope", detail: "Геометрический"},
    {value: "Nunito", detail: "Скруглённый"},
    {value: "Rubik", detail: "Мягкий геометрический"},
    {value: "Montserrat", detail: "Высокий геометрический"},
    {value: "Noto Serif", detail: "Антиква"},
    {value: "Literata", detail: "Книжная антиква"},
];

const paletteGroups: {title: string; fields: {token: PaletteToken; label: string}[]}[] = [
    {title: "Поверхности", fields: [
        {token: "window", label: "Фон"},
        {token: "card", label: "Карточки"},
        {token: "cardStrong", label: "Аватары и вложения"},
        {token: "line", label: "Границы"},
    ]},
    {title: "Текст", fields: [
        {token: "text", label: "Текст"},
        {token: "muted", label: "Вторичный текст"},
        {token: "onAccent", label: "Текст на акценте"},
    ]},
    {title: "Акценты", fields: [
        {token: "accent", label: "Акцент"},
        {token: "accentBlue", label: "Выделение"},
        {token: "danger", label: "Ошибка"},
        {token: "warning", label: "Предупреждение"},
    ]},
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
            setNotice(error instanceof Error ? error.message : "Не удалось сохранить тему");
        }
    };

    const chooseImage = async (file: File | undefined) => {
        if (!file) return;
        try {
            const stageImage = await readStageImage(file);
            commit({stageBackdrop: "image", stageImage});
        } catch (error) {
            setNotice(error instanceof Error ? error.message : "Не удалось прочитать картинку.");
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
            setNotice(error instanceof Error ? error.message : "Не удалось импортировать тему");
        } finally {
            if (importRef.current) importRef.current.value = "";
        }
    };

    const save = () => {
        try {
            onSave(name);
            setNotice("");
        } catch (error) {
            setNotice(error instanceof Error ? error.message : "Не удалось сохранить тему");
        }
    };

    return <>
        <div className="choice-heading">Темы</div>
        <div className="theme-gallery" role="group" aria-label="Темы оформления">
            {builtinThemes.map((theme) => <ThemeSwatch key={theme} label={builtinLabels[theme]} palette={themePalettes[theme]}
                                                        fontFamily="Noto Sans" pressed={appearance.theme === theme}
                                                        onClick={() => void onTheme(theme)}/>)}
            {appearance.savedThemes.map((theme) => <ThemeSwatch key={theme.id} label={theme.name} palette={theme.palette}
                                                                 fontFamily={theme.fontFamily}
                                                                 pressed={custom && appearance.activeSavedId === theme.id}
                                                                 onClick={() => onOpenSaved(theme.id)}
                                                                 onExport={() => onExport(theme)}
                                                                 onDelete={() => onDelete(theme.id)}/>)}
            {appearance.hasDraft && <ThemeSwatch label={appearance.customName || "Черновик"} palette={appearance.custom}
                                                  fontFamily={appearance.fontFamily}
                                                  pressed={custom && !appearance.activeSavedId}
                                                  onClick={() => void onTheme("custom")}/>}
        </div>
        <p className="theme-note">Встроенные темы нельзя экспортировать. Своей темой можно поделиться файлом.</p>
        <div className="theme-actions">
            <button type="button" className="primary-button theme-create" onClick={onCreate}>Создать новую тему</button>
            <button type="button" className="secondary-button" onClick={() => importRef.current?.click()}>Импорт</button>
            <input ref={importRef} type="file" accept="application/json,.json" hidden
                   onChange={(event) => void importFile(event.currentTarget.files?.[0])}/>
        </div>
        {appearance.savedThemes.length > 0 && <p className="theme-note">Новая тема копирует цвета выбранной. Сохранённые темы остаются в списке.</p>}
        {custom && <div className="theme-editor">
            <div className="theme-save">
                <label className="setting-label" htmlFor="theme-name">Название</label>
                <input id="theme-name" className="theme-name" maxLength={40} value={name} placeholder="Своя тема"
                       onChange={(event) => setName(event.target.value)}/>
                <div className="theme-actions">
                    <button type="button" className="primary-button theme-create" onClick={save}>{appearance.activeSavedId ? "Сохранить" : "Сохранить в список"}</button>
                    <button type="button" className="secondary-button" onClick={() => onExport({
                        id: appearance.activeSavedId || "draft",
                        name: cleanThemeName(name),
                        palette: appearance.custom,
                        fontFamily: appearance.fontFamily,
                        fontSize: appearance.fontSize,
                        stageBackdrop: appearance.stageBackdrop,
                        stageImage: appearance.stageBackdrop === "image" ? appearance.stageImage : "",
                    })}>Экспорт</button>
                </div>
            </div>
            <fieldset className="palette-editor">
                <legend>Цвета</legend>
                {paletteGroups.map((group) => <fieldset className="palette-group" key={group.title}>
                    <legend>{group.title}</legend>
                    <div className="palette-grid">
                        {group.fields.map((field) => {
                            const color = appearance.custom[field.token];
                            return <label className="palette-field" key={field.token}>
                                <input type="color" aria-label={field.label} value={color}
                                       onInput={(event) => commit({custom: {...appearance.custom, [field.token]: event.currentTarget.value}})}/>
                                <span>{field.label}</span>
                                <code>{color}</code>
                            </label>;
                        })}
                    </div>
                </fieldset>)}
            </fieldset>
            <div>
                <div className="choice-heading" id="font-family-label">Шрифт</div>
                <div className="font-grid" role="group" aria-labelledby="font-family-label">
                    {fontChoices.map((font) => <button type="button" className="font-option" key={font.value}
                                                         aria-pressed={appearance.fontFamily === font.value}
                                                         style={{fontFamily: fontStack(font.value)}}
                                                         onClick={() => {
                                                             if (fontFamilies.some((family) => family === font.value)) commit({fontFamily: font.value});
                                                         }}>
                        <strong>{font.value}</strong>
                        <span>Съешь ещё этих мягких булок</span>
                    </button>)}
                </div>
            </div>
            <div>
                <div className="choice-heading" id="font-size-label">Размер шрифта</div>
                <div className="size-row" role="group" aria-labelledby="font-size-label">
                    {fontSizes.map((size) => <button type="button" className="size-chip" key={size} aria-pressed={appearance.fontSize === size}
                                                      style={{fontSize: size}}
                                                      onClick={() => commit({fontSize: size})}>{size}</button>)}
                </div>
                <p className="theme-note">Только размеры, которые не ломают раскладку.</p>
            </div>
            <div>
                <div className="choice-heading" id="backdrop-label">Подложка под аватарками</div>
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
                        <span>{option.label}</span>
                    </button>)}
                </div>
            </div>
            {appearance.stageBackdrop === "image" && <div className="stage-image-picker">
                <button type="button" className="secondary-button" onClick={() => imageRef.current?.click()}>Выбрать картинку</button>
                <input ref={imageRef} type="file" accept="image/png,image/jpeg,image/webp" hidden
                       onChange={(event) => void chooseImage(event.currentTarget.files?.[0])}/>
                {stageImageBackground(appearance.stageImage) && <img className="stage-image-preview" alt="" src={appearance.stageImage}/>}
                <p className="theme-note">PNG, JPEG или WebP до 1,2 МБ. Файл остаётся на этом компьютере.</p>
            </div>}
            {notice && <div className="form-error" role="alert">{notice}</div>}
        </div>}
        {!custom && notice && <div className="form-error" role="alert">{notice}</div>}
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
    return <div className="theme-swatch">
        <button type="button" className="theme-swatch-open" aria-pressed={pressed} onClick={onClick}>
            <span className="theme-swatch-canvas" style={{background: palette.window}}>
                <span className="theme-swatch-card" style={{background: palette.card, borderColor: palette.line, color: palette.text, fontFamily: fontStack(fontFamily)}}>
                    <i style={{background: palette.accent}}/>
                    <b>Аа</b>
                </span>
            </span>
            <span className="theme-swatch-label">{label}</span>
        </button>
        {(onExport || onDelete) && <div className="theme-swatch-actions">
            {onExport && <button type="button" className="text-button" onClick={onExport}>Экспорт</button>}
            {onDelete && <button type="button" className="text-button" onClick={onDelete}>Удалить</button>}
        </div>}
    </div>;
}
