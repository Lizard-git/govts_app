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
    ScreenStreamDTO,
    MediaTrustDTO,
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
    mediaServerIdentity: () => Service.MediaServerIdentity(),
    trustMediaServer: (fingerprint: string) => Service.TrustMediaServer(fingerprint),
    publishScreen: (offer: {type: string; sdp: string}) => Service.PublishScreen(offer),
    subscribeScreen: (streamID: string, subscriberID: string, offer: {type: string; sdp: string}) =>
        Service.SubscribeScreen(streamID, subscriberID, offer),
    stopScreen: (streamID: string) => Service.StopScreen(streamID),
    unsubscribeScreen: (streamID: string, subscriberID: string) => Service.UnsubscribeScreen(streamID, subscriberID),
    openScreenWindow: (streamID: string, ownerName: string) => Service.OpenScreenWindow(streamID, ownerName),
    onStateChanged: (listener: () => void) => Events.On("client-state-changed", listener),
    onEventLogChanged: (listener: () => void) => Events.On("client-event-log-changed", listener),
    onAudioMeter: (listener: (sample: AudioMeterDTO) => void) =>
        Events.On("audio-meter", (event) => listener(event.data as AudioMeterDTO)),
};
