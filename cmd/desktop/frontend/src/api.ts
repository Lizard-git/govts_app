import {Events} from "@wailsio/runtime";
import {Service} from "../bindings/example.com/go-voice-mvp/internal/ui/wails";

export type {
    AudioDeviceDTO,
    AudioDevicesDTO,
    AudioMeterDTO,
    ChannelDTO,
    ClientEventDTO,
    ClientViewDTO,
    ConnectRequest,
    ParticipantDTO,
} from "../bindings/example.com/go-voice-mvp/internal/ui/wails";

import type {AudioMeterDTO, ConnectRequest} from "../bindings/example.com/go-voice-mvp/internal/ui/wails";

export const desktopAPI = {
    snapshot: () => Service.Snapshot(),
    eventsAfter: (sequence: string) => Service.EventsAfter(sequence),
    savedDisplayName: () => Service.SavedDisplayName(),
    connect: (request: ConnectRequest) => Service.Connect(request),
    disconnect: () => Service.Disconnect(),
    joinChannel: (channelID: string) => Service.JoinChannel(channelID),
    setMuted: (value: boolean) => Service.SetMuted(value),
    setDeafened: (value: boolean) => Service.SetDeafened(value),
    setRNNoiseEnabled: (value: boolean) => Service.SetRNNoiseEnabled(value),
    setRNNoiseSensitivity: (value: number) => Service.SetRNNoiseSensitivity(value),
    setVADEnabled: (value: boolean) => Service.SetVADEnabled(value),
    setVADMode: (value: string) => Service.SetVADMode(value),
    setVADSensitivity: (value: number) => Service.SetVADSensitivity(value),
    audioDevices: () => Service.AudioDevices(),
    setCaptureDevice: (id: string) => Service.SetCaptureDevice(id),
    setPlaybackDevice: (id: string) => Service.SetPlaybackDevice(id),
    onStateChanged: (listener: () => void) => Events.On("client-state-changed", listener),
    onEventLogChanged: (listener: () => void) => Events.On("client-event-log-changed", listener),
    onAudioMeter: (listener: (sample: AudioMeterDTO) => void) =>
        Events.On("audio-meter", (event) => listener(event.data as AudioMeterDTO)),
};
