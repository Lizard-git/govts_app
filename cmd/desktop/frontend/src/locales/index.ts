import {en} from "./en";
import {ky} from "./ky";
import {ru} from "./ru";
import type {Messages} from "./types";

export type {Messages} from "./types";

// A language is a catalog file plus one entry here.
// The id is a short lowercase code (ISO 639, so Kyrgyz is "ky").
// Go stores that code and does not keep its own list.
export type LocaleDefinition = {
    id: string;
    nativeName: string;
    shortName: string;
    htmlLang: string;
    messages: Messages;
};

export const locales: LocaleDefinition[] = [
    {id: "ru", nativeName: "Русский", shortName: "Ру", htmlLang: "ru", messages: ru},
    {id: "en", nativeName: "English", shortName: "En", htmlLang: "en", messages: en},
    {id: "ky", nativeName: "Кыргызча", shortName: "Кы", htmlLang: "ky", messages: ky},
];

export const fallbackLocaleId = "ru";

export function localeById(id: string): LocaleDefinition {
    return locales.find((item) => item.id === id) ?? locales.find((item) => item.id === fallbackLocaleId) ?? locales[0];
}

export function errorCode(key: keyof Messages["errors"]): keyof Messages["errors"] {
    return key;
}

const mediaKeyPrefix = "media-key-changed:";

export function rawError(error: unknown): string {
    const raw = typeof error === "string" ? error : error instanceof Error ? error.message : String(error ?? "");
    return raw.replace(/^Error:\s*/, "");
}

export function errorText(error: unknown, messages: Messages): string {
    const raw = rawError(error);
    if (raw.startsWith(mediaKeyPrefix)) return messages.screen.mediaKeyChanged(raw.slice(mediaKeyPrefix.length));
    return messages.errors[raw as keyof Messages["errors"]] ?? raw;
}

export function mediaKeyError(fingerprint: string): string {
    return `${mediaKeyPrefix}${fingerprint}`;
}
