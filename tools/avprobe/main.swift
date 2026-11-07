import AVFoundation
import CoreVideo
import Foundation

// avprobe plays one HLS URL in AVPlayer for a fixed time and prints one JSON
// object describing what a viewer would have seen. One URL per process keeps
// results attributable. It must run outside any sandbox: sandboxed processes
// fail every item with CoreMedia -9405.
//
// Usage: avprobe URL duration-seconds

guard CommandLine.arguments.count == 3, let url = URL(string: CommandLine.arguments[1]),
      let seconds = Int(CommandLine.arguments[2]), seconds > 0 else {
    fputs("usage: avprobe URL duration-seconds\n", stderr)
    exit(2)
}

final class Probe {
    let url: URL
    let item: AVPlayerItem
    let player: AVPlayer
    let output = AVPlayerItemVideoOutput(pixelBufferAttributes: [kCVPixelBufferPixelFormatTypeKey as String: kCVPixelFormatType_32BGRA])
    let started = Date()
    var frames: [Int]
    var playheads: [Double?]
    var sizes = Set<String>()
    var stalls: [Double] = []
    var failures: [[String: Any]] = []
    var firstFrame: Double?
    var lastFrame: Double?
    // Gaps of at least 0.5 s between delivered frames after the first frame.
    var gaps: [[String: Double]] = []
    var statusLog: [[String: Any]] = []
    var observers: [NSObjectProtocol] = []
    var statusObservation: NSKeyValueObservation?
    var keepUpObservation: NSKeyValueObservation?

    init(url: URL, seconds: Int) {
        self.url = url
        item = AVPlayerItem(url: url)
        player = AVPlayer(playerItem: item)
        frames = Array(repeating: 0, count: seconds)
        playheads = Array(repeating: nil, count: seconds)
        item.add(output)
        player.isMuted = true
        player.automaticallyWaitsToMinimizeStalling = true
        observers.append(NotificationCenter.default.addObserver(forName: .AVPlayerItemPlaybackStalled, object: item, queue: .main) { [weak self] _ in
            guard let self else { return }
            self.stalls.append(self.now())
        })
        observers.append(NotificationCenter.default.addObserver(forName: .AVPlayerItemFailedToPlayToEndTime, object: item, queue: .main) { [weak self] n in
            guard let self else { return }
            if let error = n.userInfo?[AVPlayerItemFailedToPlayToEndTimeErrorKey] as? NSError {
                self.failures.append(self.describe(error))
            }
        })
        statusObservation = item.observe(\.status, options: [.new]) { [weak self] item, _ in
            guard let self else { return }
            var entry: [String: Any] = ["at": self.now(), "status": item.status == .readyToPlay ? "ready" : (item.status == .failed ? "failed" : "unknown")]
            if let error = item.error as NSError? { entry["error"] = self.describe(error) }
            self.statusLog.append(entry)
        }
        player.play()
    }

    func now() -> Double { Date().timeIntervalSince(started) }

    func describe(_ error: NSError) -> [String: Any] {
        var out: [String: Any] = ["domain": error.domain, "code": error.code, "message": error.localizedDescription]
        if let underlying = error.userInfo[NSUnderlyingErrorKey] as? NSError {
            out["underlying"] = ["domain": underlying.domain, "code": underlying.code, "message": underlying.localizedDescription]
        }
        return out
    }

    func tick() {
        let t = now()
        let index = Int(t)
        guard index >= 0 && index < frames.count else { return }
        let time = item.currentTime()
        if time.seconds.isFinite { playheads[index] = time.seconds }
        var delivered = 0
        while output.hasNewPixelBuffer(forItemTime: time), let pb = output.copyPixelBuffer(forItemTime: time, itemTimeForDisplay: nil) {
            delivered += 1
            sizes.insert("\(CVPixelBufferGetWidth(pb))x\(CVPixelBufferGetHeight(pb))")
            if delivered > 120 { break }
        }
        guard delivered > 0 else { return }
        frames[index] += delivered
        if firstFrame == nil { firstFrame = t }
        if let last = lastFrame, t - last >= 0.5 { gaps.append(["from": last, "to": t]) }
        lastFrame = t
    }

    func finish() {
        let end = now()
        if let last = lastFrame, end - last >= 0.5 { gaps.append(["from": last, "to": end, "open": 1]) }
        let events = (item.errorLog()?.events ?? []).map { e -> [String: Any] in
            ["code": e.errorStatusCode, "domain": e.errorDomain, "comment": e.errorComment ?? "", "uri": e.uri.map { URL(string: $0)?.path ?? "" } ?? "", "at": e.date.map { $0.timeIntervalSince(started) } ?? -1]
        }
        let access = (item.accessLog()?.events ?? []).map { e -> [String: Any] in
            ["stalls": e.numberOfStalls, "droppedVideoFrames": e.numberOfDroppedVideoFrames, "segmentsDownloaded": e.numberOfMediaRequests, "startupTime": e.startupTime, "observedBitrate": e.observedBitrate]
        }
        var frozen = 0.0
        for g in gaps { frozen += (g["to"] ?? 0) - (g["from"] ?? 0) }
        var playheadProgress: Double?
        if let first = playheads.first(where: { $0 != nil }) ?? nil, let last = playheads.last(where: { $0 != nil }) ?? nil { playheadProgress = last - first }
        var result: [String: Any] = [
            "url": url.path,
            "durationSeconds": end,
            "framesPerSecond": frames,
            "playheadSeconds": playheads.map { $0 as Any? ?? NSNull() },
            "firstFrameSeconds": firstFrame as Any? ?? NSNull(),
            "lastFrameSeconds": lastFrame as Any? ?? NSNull(),
            "frozenSeconds": firstFrame == nil ? NSNull() : frozen as Any,
            "gaps": gaps,
            "stallNotifications": stalls,
            "failures": failures,
            "statusLog": statusLog,
            "errorLog": events,
            "accessLog": access,
            "sizes": sizes.sorted(),
            "playheadProgressSeconds": playheadProgress as Any? ?? NSNull(),
            "finalStatus": item.status == .readyToPlay ? "ready" : (item.status == .failed ? "failed" : "unknown"),
            "audio": "not measured in-player: AVAudioMix/MTAudioProcessingTap cannot attach to HLS items",
        ]
        if let error = item.error as NSError? { result["itemError"] = describe(error) }
        do {
            let data = try JSONSerialization.data(withJSONObject: result, options: [.sortedKeys])
            print(String(decoding: data, as: UTF8.self))
        } catch {
            fputs("JSON: \(error)\n", stderr)
            exit(1)
        }
        player.pause()
        observers.forEach { NotificationCenter.default.removeObserver($0) }
    }
}

let probe = Probe(url: url, seconds: seconds)
Timer.scheduledTimer(withTimeInterval: 0.005, repeats: true) { _ in probe.tick() }
Timer.scheduledTimer(withTimeInterval: Double(seconds), repeats: false) { _ in
    probe.finish()
    exit(0)
}
RunLoop.main.run()
