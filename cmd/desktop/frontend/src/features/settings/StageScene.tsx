import type {StageBackdrop} from "../../appearance";

const stars: {x: number; y: number; size: number; opacity: number}[] = [
    {x: 4, y: 18, size: 1.5, opacity: 0.95},
    {x: 8, y: 62, size: 0.55, opacity: 0.35},
    {x: 13, y: 28, size: 0.9, opacity: 0.6},
    {x: 18, y: 74, size: 0.5, opacity: 0.3},
    {x: 22, y: 14, size: 2, opacity: 1},
    {x: 27, y: 42, size: 0.7, opacity: 0.45},
    {x: 33, y: 22, size: 1.15, opacity: 0.7},
    {x: 38, y: 66, size: 0.6, opacity: 0.35},
    {x: 43, y: 12, size: 0.75, opacity: 0.5},
    {x: 47, y: 48, size: 1.8, opacity: 0.95},
    {x: 53, y: 26, size: 0.55, opacity: 0.35},
    {x: 58, y: 70, size: 0.85, opacity: 0.5},
    {x: 63, y: 16, size: 1.3, opacity: 0.8},
    {x: 69, y: 40, size: 0.6, opacity: 0.4},
    {x: 74, y: 20, size: 2.1, opacity: 1},
    {x: 79, y: 58, size: 0.7, opacity: 0.45},
    {x: 84, y: 14, size: 1.05, opacity: 0.65},
    {x: 89, y: 36, size: 0.55, opacity: 0.35},
    {x: 93, y: 68, size: 1.4, opacity: 0.75},
    {x: 97, y: 22, size: 0.65, opacity: 0.4},
];

function mulberry32(seed: number) {
    return () => {
        seed |= 0;
        seed = (seed + 0x6D2B79F5) | 0;
        let value = Math.imul(seed ^ (seed >>> 15), 1 | seed);
        value = (value + Math.imul(value ^ (value >>> 7), 61 | value)) ^ value;
        return ((value ^ (value >>> 14)) >>> 0) / 4294967296;
    };
}

const pines: {x: number; h: number; w: number; far: boolean}[] = [];
{
    const random = mulberry32(0x51a7c3);
    let x = -16;
    while (x < 1700) {
        const h = 18 + random() * 64;
        const w = 6 + random() * 28;
        pines.push({x, h: Math.round(h), w: Math.round(w), far: random() > 0.58});
        const clearing = random() < 0.14;
        x += (clearing ? 22 + random() * 36 : 2 + random() * 14);
    }
}

const buildings: {x: number; h: number; w: number; far: boolean}[] = [];
{
    const random = mulberry32(0xc17e11);
    let x = -6;
    while (x < 1700) {
        const h = 14 + Math.pow(random(), 1.4) * 78;
        const w = 7 + random() * 20;
        buildings.push({x, h: Math.round(h), w: Math.round(w), far: random() > 0.5});
        x += w * 0.2 + (random() < 0.18 ? 8 + random() * 16 : random() * 3);
    }
}

function shownScene(backdrop: StageBackdrop): StageBackdrop {
    const value: string = backdrop;
    if (value === "dunes") return "pines";
    if (value === "waves") return "clouds";
    if (value === "aurora") return "city";
    return backdrop;
}

export function StageScene({backdrop}: {backdrop: StageBackdrop}) {
    const scene = shownScene(backdrop);
    if (scene === "plain" || scene === "image") return null;
    return <div className={`stage-scene stage-scene-${scene}`} aria-hidden="true">
        {scene === "mountains" && <div className="stage-ridge"/>}
        {scene === "pines" && <div className="stage-pines">{pines.map((pine, index) => <i key={index} className={pine.far ? "stage-pine stage-pine-far" : "stage-pine"} style={{left: `${pine.x}cqh`, height: `${pine.h}cqh`, width: `${pine.w}cqh`}}/>)}</div>}
        {scene === "clouds" && <><i className="stage-cloud stage-cloud-a"/><i className="stage-cloud stage-cloud-b"/><i className="stage-cloud stage-cloud-c"/></>}
        {scene === "horizon" && <><div className="stage-sun"/><div className="stage-ground"/></>}
        {scene === "city" && <div className="stage-city">{buildings.map((building, index) => <i key={index} className={building.far ? "stage-building stage-building-far" : "stage-building"} style={{left: `${building.x}cqh`, height: `${building.h}cqh`, width: `${building.w}cqh`}}/>)}</div>}
        {scene === "stars" && <div className="stage-stars">{stars.map((star) => <i key={`${star.x}-${star.y}`} className={star.size >= 1.7 ? "stage-star-bright" : undefined} style={{left: `${star.x}%`, top: `${star.y}%`, width: `${star.size}cqh`, height: `${star.size}cqh`, opacity: star.opacity}}/>)}</div>}
    </div>;
}
