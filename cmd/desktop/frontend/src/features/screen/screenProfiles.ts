export type ScreenProfileID = "economy" | "text" | "standard";

export type ScreenProfile = {
    id: ScreenProfileID;
    label: string;
    width: number;
    height: number;
    minFrameRate: number;
    frameRate: number;
    bitrate: number;
    contentHint: "detail" | "motion";
    degradationPreference: "maintain-framerate" | "maintain-resolution";
};

export const screenProfiles: ScreenProfile[] = [
    {id: "economy", label: "Экономный · 720p/15", width: 1280, height: 720, minFrameRate: 10, frameRate: 15, bitrate: 1_000_000, contentHint: "motion", degradationPreference: "maintain-framerate"},
    {id: "text", label: "Текст · 1080p/15", width: 1920, height: 1080, minFrameRate: 10, frameRate: 15, bitrate: 2_000_000, contentHint: "detail", degradationPreference: "maintain-resolution"},
    {id: "standard", label: "Стандартный · 1080p/30", width: 1920, height: 1080, minFrameRate: 15, frameRate: 30, bitrate: 2_500_000, contentHint: "motion", degradationPreference: "maintain-framerate"},
];

export const defaultScreenProfile = screenProfiles[2];
