import AVFoundation
import CoreVideo
import Foundation

// avvod plays one VOD HLS URL in AVPlayer and runs a client script against it:
// seeks, pause/play, and replacing the item with a new URL (how a Plex client
// restarts a transcode for a track or quality change). It prints one JSON
// object. Run it outside any sandbox (inside one, every item fails -9405).
//
// Usage: avvod URL duration-seconds [script]
//
// script is ';'-separated steps "AT|ACTION[|ARG]", AT in seconds since start:
//   5|seek|40       seek to 40 s of item time (exact, zero tolerance)
//   5|seek|end-3    seek to 3 s before the item's end
//   9|pause  9|play
//   12|replace|URL  replace the current item with URL (expected content
//                   position in ARG2: 12|replace|URL|offset-seconds)
//
// Frames of the Ether sync fixture carry a 16-bit frame counter barcode
// (bottom strip) and a green top band; for those, every delivered frame is
// mapped to its content time, so a seek's landing is measured against the
// picture actually shown, not only the playhead.

let args = CommandLine.arguments
guard args.count >= 3, let firstURL = URL(string: args[1]), let seconds = Double(args[2]), seconds > 0 else {
    fputs("usage: avvod URL duration-seconds [script]\n", stderr)
    exit(2)
}

struct Step {
    let at: Double
    let action: String
    let arg: String
    let arg2: String
}

let steps: [Step] = (args.count > 3 ? args[3] : "").split(separator: ";").compactMap { raw in
    let f = raw.split(separator: "|", maxSplits: 3, omittingEmptySubsequences: false).map(String.init)
    guard f.count >= 2, let at = Double(f[0]) else { return nil }
    return Step(at: at, action: f[1], arg: f.count > 2 ? f[2] : "", arg2: f.count > 3 ? f[3] : "")
}.sorted { $0.at < $1.at }

let fixtureFPS = 30.0

final class VOD {
    let player = AVPlayer()
    var item: AVPlayerItem!
    var output: AVPlayerItemVideoOutput!
    let started = Date()
    var itemIndex = -1
    var items: [[String: Any]] = []
    var actions: [[String: Any]] = []
    var pending: Int? // index into actions awaiting its first frame
    var pendingSince = 0.0
    var pendingReady = false
    var samples: [[String: Any]] = []
    var lastSampleAt = -1.0
    var frames = 0
    var framesPerSecond: [Int]
    var lastFrameAt: Double?
    var gaps: [[String: Double]] = []
    var stalls: [Double] = []
    var ended: [Double] = []
    var failures: [[String: Any]] = []
    var errorLog: [[String: Any]] = []
    var statusLog: [[String: Any]] = []
    var observers: [NSObjectProtocol] = []
    var statusObservation: NSKeyValueObservation?
    var lastContent: Int?
    var contentJumps: [[String: Any]] = []
    var offsets: [Int: [Double]] = [:] // per item: content time minus item time, per decoded frame
    var paused = false

    init(seconds: Double) {
        framesPerSecond = Array(repeating: 0, count: Int(seconds.rounded(.up)) + 1)
        player.isMuted = true
        player.automaticallyWaitsToMinimizeStalling = true
    }

    func now() -> Double { Date().timeIntervalSince(started) }

    func describe(_ error: NSError) -> [String: Any] {
        var out: [String: Any] = ["domain": error.domain, "code": error.code, "message": error.localizedDescription]
        if let u = error.userInfo[NSUnderlyingErrorKey] as? NSError {
            out["underlying"] = ["domain": u.domain, "code": u.code, "message": u.localizedDescription]
        }
        return out
    }

    func load(_ url: URL) {
        if let old = item { collectLogs(old) }
        observers.forEach { NotificationCenter.default.removeObserver($0) }
        observers.removeAll()
        itemIndex += 1
        let it = AVPlayerItem(url: url)
        let out = AVPlayerItemVideoOutput(pixelBufferAttributes: [kCVPixelBufferPixelFormatTypeKey as String: kCVPixelFormatType_32BGRA])
        it.add(out)
        item = it
        output = out
        lastContent = nil
        items.append(["index": itemIndex, "url": url.path, "query": url.query ?? "", "loadedAt": now()])
        let idx = itemIndex
        observers.append(NotificationCenter.default.addObserver(forName: .AVPlayerItemPlaybackStalled, object: it, queue: .main) { [weak self] _ in
            guard let self else { return }
            self.stalls.append(self.now())
        })
        observers.append(NotificationCenter.default.addObserver(forName: .AVPlayerItemDidPlayToEndTime, object: it, queue: .main) { [weak self] _ in
            guard let self else { return }
            self.ended.append(self.now())
        })
        observers.append(NotificationCenter.default.addObserver(forName: .AVPlayerItemFailedToPlayToEndTime, object: it, queue: .main) { [weak self] n in
            guard let self else { return }
            if let e = n.userInfo?[AVPlayerItemFailedToPlayToEndTimeErrorKey] as? NSError {
                var d = self.describe(e)
                d["item"] = idx
                d["at"] = self.now()
                self.failures.append(d)
            }
        })
        statusObservation = it.observe(\.status, options: [.new]) { [weak self] it, _ in
            guard let self else { return }
            var entry: [String: Any] = ["at": self.now(), "item": idx, "status": it.status == .readyToPlay ? "ready" : (it.status == .failed ? "failed" : "unknown")]
            if it.status == .readyToPlay {
                entry["duration"] = it.duration.seconds.isFinite ? it.duration.seconds : -1
                if let p = self.pending, (self.actions[p]["action"] as? String) == "replace" || (self.actions[p]["action"] as? String) == "start" {
                    self.pendingReady = true
                    self.actions[p]["readyAt"] = self.now()
                }
            }
            if let e = it.error as NSError? { entry["error"] = self.describe(e) }
            self.statusLog.append(entry)
        }
        player.replaceCurrentItem(with: it)
        if !paused { player.play() }
    }

    func collectLogs(_ it: AVPlayerItem) {
        let idx = itemIndex
        for e in it.errorLog()?.events ?? [] {
            errorLog.append(["item": idx, "code": e.errorStatusCode, "domain": e.errorDomain, "comment": e.errorComment ?? "", "uri": e.uri.map { URL(string: $0)?.path ?? "" } ?? "", "at": e.date.map { $0.timeIntervalSince(started) } ?? -1])
        }
        if let e = it.error as NSError? {
            var d = describe(e)
            d["item"] = idx
            failures.append(d)
        }
    }

    func begin(_ p: Int) {
        pending = p
        pendingSince = now()
        pendingReady = false
    }

    func run(_ s: Step) {
        var a: [String: Any] = ["at": s.at, "issuedAt": now(), "action": s.action, "arg": s.arg]
        switch s.action {
        case "seek":
            var target = Double(s.arg) ?? 0
            if s.arg.hasPrefix("end-"), let back = Double(s.arg.dropFirst(4)), item.duration.seconds.isFinite {
                target = item.duration.seconds - back
            }
            target = max(0, target)
            a["target"] = target
            actions.append(a)
            let p = actions.count - 1
            begin(p)
            item.seek(to: CMTime(seconds: target, preferredTimescale: 90000), toleranceBefore: .zero, toleranceAfter: .zero) { [weak self] done in
                guard let self else { return }
                self.actions[p]["completedAt"] = self.now()
                self.actions[p]["finished"] = done
                if self.pending == p { self.pendingReady = true }
                // A client resumes after a seek, also from the end of the item.
                if done && !self.paused { self.player.play() }
            }
        case "pause":
            paused = true
            player.pause()
            a["playhead"] = item.currentTime().seconds
            actions.append(a)
        case "play":
            paused = false
            player.play()
            a["playhead"] = item.currentTime().seconds
            actions.append(a)
            begin(actions.count - 1)
            pendingReady = true
        case "replace":
            guard let u = URL(string: s.arg) else { return }
            a["arg"] = u.path
            a["query"] = u.query ?? ""
            if let t = Double(s.arg2) { a["target"] = t }
            actions.append(a)
            begin(actions.count - 1)
            load(u)
        default:
            actions.append(a)
        }
    }

    func tick() {
        let t = now()
        guard let it = item, let out = output else { return }
        let time = it.currentTime()
        var delivered = 0
        var displayTime = CMTime.invalid
        while out.hasNewPixelBuffer(forItemTime: time), let pb = out.copyPixelBuffer(forItemTime: time, itemTimeForDisplay: &displayTime) {
            delivered += 1
            let shown = displayTime.seconds
            let content = barcode(pb)
            if let c = content {
                if shown.isFinite { offsets[itemIndex, default: []].append(Double(c) / fixtureFPS - shown) }
                if let last = lastContent, c != last + 1, c != last {
                    contentJumps.append(["at": t, "from": last, "to": c, "itemTime": shown.isFinite ? shown : -1])
                }
                lastContent = c
            }
            if let p = pending, pendingReady || t - pendingSince > 20 {
                actions[p]["firstFrameAt"] = t
                actions[p]["resumeSeconds"] = t - (actions[p]["issuedAt"] as? Double ?? t)
                if shown.isFinite { actions[p]["itemTimeAtFirstFrame"] = shown }
                if let c = content { actions[p]["contentAtFirstFrame"] = Double(c) / fixtureFPS }
                pending = nil
            }
            if delivered > 120 { break }
        }
        if t - lastSampleAt >= 0.5 {
            lastSampleAt = t
            var s: [String: Any] = ["at": t, "item": itemIndex, "itemTime": time.seconds.isFinite ? time.seconds : -1]
            // The contiguous loaded range containing the playhead is the player
            // runway; a later disconnected range must not hide a buffer hole.
            var runway = 0.0
            var loaded: [[String: Double]] = []
            for value in it.loadedTimeRanges {
                let range = value.timeRangeValue
                let start = range.start.seconds
                let end = CMTimeRangeGetEnd(range).seconds
                if start.isFinite && end.isFinite {
                    loaded.append(["start": start, "end": end])
                    if time.seconds.isFinite && start <= time.seconds && end >= time.seconds {
                        runway = max(runway, end - time.seconds)
                    }
                }
            }
            s["bufferAheadSeconds"] = runway
            s["loadedRanges"] = loaded
            s["bufferEmpty"] = it.isPlaybackBufferEmpty

            switch player.timeControlStatus {
            case .playing: s["control"] = "playing"
            case .paused: s["control"] = "paused"
            default: s["control"] = "waiting:" + (player.reasonForWaitingToPlay?.rawValue ?? "")
            }
            if let c = lastContent { s["content"] = Double(c) / fixtureFPS }
            samples.append(s)
        }
        guard delivered > 0 else { return }
        frames += delivered
        let i = min(Int(t), framesPerSecond.count - 1)
        framesPerSecond[i] += delivered
        // Gaps only count while the player should be presenting frames.
        if let last = lastFrameAt, t - last >= 0.5, !paused { gaps.append(["from": last, "to": t]) }
        lastFrameAt = t
    }

    // barcode reads the sync fixture's frame counter: a white guard box then
    // 16 bit boxes (LSB first) on a black strip at 71-77% of the height. nil
    // when the strip is absent (another source, an Ether screen).
    func barcode(_ pb: CVPixelBuffer) -> Int? {
        CVPixelBufferLockBaseAddress(pb, .readOnly)
        defer { CVPixelBufferUnlockBaseAddress(pb, .readOnly) }
        guard let raw = CVPixelBufferGetBaseAddress(pb) else { return nil }
        let base = raw.assumingMemoryBound(to: UInt8.self)
        let w = CVPixelBufferGetWidth(pb), h = CVPixelBufferGetHeight(pb), bpr = CVPixelBufferGetBytesPerRow(pb)
        func luma(_ fx: Double, _ fy: Double) -> Int {
            let x = min(w - 1, max(0, Int(fx * Double(w)))), y = min(h - 1, max(0, Int(fy * Double(h))))
            let p = base + y * bpr + x * 4
            return (Int(p[0]) + Int(p[1]) + Int(p[2])) / 3
        }
        let y = 0.74
        // Guard box at slot -1 is white; the strip between boxes is black.
        guard luma(0.08 - 0.052 + 0.02, y) > 160, luma(0.08 - 0.006, y) < 80 else { return nil }
        var n = 0
        for i in 0..<16 {
            if luma(0.08 + Double(i) * 0.052 + 0.02, y) > 128 { n |= 1 << i }
        }
        return n
    }

    func finish() {
        if let it = item { collectLogs(it) }
        let end = now()
        if let last = lastFrameAt, end - last >= 0.5, !paused { gaps.append(["from": last, "to": end, "open": 1]) }
        if let p = pending { actions[p]["firstFrameAt"] = NSNull() }
        var offsetSummary: [[String: Any]] = []
        for (idx, list) in offsets.sorted(by: { $0.key < $1.key }) {
            let o = list.sorted()
            offsetSummary.append(["item": idx, "n": o.count, "median": o[o.count / 2], "min": o.first!, "max": o.last!])
        }
        let result: [String: Any] = [
            "durationSeconds": end,
            "startedUnixSeconds": started.timeIntervalSince1970,
            "frames": frames,
            "framesPerSecond": framesPerSecond,
            "items": items,
            "actions": actions,
            "samples": samples,
            "gaps": gaps,
            "stallNotifications": stalls,
            "endedAt": ended,
            "failures": failures,
            "errorLog": errorLog,
            "statusLog": statusLog,
            "contentJumps": contentJumps,
            "contentOffsets": offsetSummary,
            "finalStatus": item?.status == .readyToPlay ? "ready" : (item?.status == .failed ? "failed" : "unknown"),
        ]
        do {
            let data = try JSONSerialization.data(withJSONObject: result, options: [.sortedKeys])
            print(String(decoding: data, as: UTF8.self))
        } catch {
            fputs("JSON: \(error)\n", stderr)
            exit(1)
        }
        player.pause()
    }
}

let vod = VOD(seconds: seconds)
vod.actions.append(["at": 0.0, "issuedAt": 0.0, "action": "start", "arg": firstURL.path])
vod.begin(0)
vod.load(firstURL)
var next = 0
Timer.scheduledTimer(withTimeInterval: 0.005, repeats: true) { _ in
    while next < steps.count && vod.now() >= steps[next].at {
        vod.run(steps[next])
        next += 1
    }
    vod.tick()
}
Timer.scheduledTimer(withTimeInterval: seconds, repeats: false) { _ in
    vod.finish()
    exit(0)
}
RunLoop.main.run()
