import AVFoundation
import Foundation
import CoreVideo

// One URL per process makes the output attributable to one player and one run.
guard CommandLine.arguments.count == 3, let url = URL(string: CommandLine.arguments[1]),
      let seconds = Int(CommandLine.arguments[2]), seconds > 0 else {
    fputs("usage: avprobe URL duration-seconds\n", stderr); exit(2)
}
final class Probe {
    let url: URL
    let item: AVPlayerItem
    let player: AVPlayer
    let output = AVPlayerItemVideoOutput(pixelBufferAttributes: [kCVPixelBufferPixelFormatTypeKey as String: kCVPixelFormatType_32BGRA])
    var frames: [Int]
    var playheads: [Double?]
    var stalls = 0
    var failures: [String] = []
    var observers: [NSObjectProtocol] = []
    let started = Date()
    init(url: URL, seconds: Int) {
        self.url = url
        item = AVPlayerItem(url: url)
        player = AVPlayer(playerItem: item)
        frames = Array(repeating: 0, count: seconds)
        playheads = Array(repeating: nil, count: seconds)
        item.add(output)
        observers.append(NotificationCenter.default.addObserver(forName: .AVPlayerItemPlaybackStalled, object: item, queue: .main) { [weak self] _ in self?.stalls += 1 })
        observers.append(NotificationCenter.default.addObserver(forName: .AVPlayerItemFailedToPlayToEndTime, object: item, queue: .main) { [weak self] n in
            if let error = n.userInfo?[AVPlayerItemFailedToPlayToEndTimeErrorKey] as? NSError { self?.failures.append("\(error.domain):\(error.code): \(error.localizedDescription)") }
        })
        player.play()
    }
    func tick() {
        let index = Int(Date().timeIntervalSince(started))
        guard index >= 0 && index < frames.count else { return }
        let time = item.currentTime()
        if time.seconds.isFinite { playheads[index] = time.seconds }
        if output.hasNewPixelBuffer(forItemTime: time), output.copyPixelBuffer(forItemTime: time, itemTimeForDisplay: nil) != nil { frames[index] += 1 }
    }
    func finish() {
        let error = item.error as NSError?
        let events = (item.errorLog()?.events ?? []).map { ["code": $0.errorStatusCode, "comment": $0.errorComment ?? ""] as [String: Any] }
        let first = frames.firstIndex(where: { $0 > 0 })
        let frozen = first.map { frames[$0...].filter { $0 == 0 }.count }
        let result: [String: Any] = ["url": url.absoluteString, "framesPerSecond": frames, "playheadSeconds": playheads.map { $0 as Any? ?? NSNull() }, "firstFrameSecond": first as Any? ?? NSNull(), "zeroFrameSecondsAfterStart": frozen as Any? ?? NSNull(), "stalls": stalls, "failures": failures, "itemError": error.map { "\($0.domain):\($0.code): \($0.localizedDescription)" } as Any? ?? NSNull(), "errorLog": events, "audioSilenceSeconds": NSNull(), "audioMeasurement": "unavailable"]
        do { let data = try JSONSerialization.data(withJSONObject: result, options: [.prettyPrinted, .sortedKeys]); print(String(decoding: data, as: UTF8.self)) } catch { fputs("JSON: \(error)\n", stderr); exit(1) }
        player.pause()
        observers.forEach { NotificationCenter.default.removeObserver($0) }
    }
}
let probe = Probe(url: url, seconds: seconds)
Timer.scheduledTimer(withTimeInterval: 0.01, repeats: true) { _ in probe.tick() }
Timer.scheduledTimer(withTimeInterval: Double(seconds), repeats: false) { _ in probe.finish(); exit(0) }
RunLoop.main.run()
