import Foundation

/// How long to wait before retrying a failed page load.
///
/// Ramps rather than polling at a flat interval: a dropped wifi frame is back
/// in two seconds, while a server that is genuinely down settles into a ten
/// second poll instead of hammering it. Pure, so the schedule is testable
/// without a timer.
enum ReloadPolicy {
    static let schedule: [TimeInterval] = [2, 5, 10]

    /// - Parameter afterFailures: consecutive failures so far, 1 for the first.
    static func delay(afterFailures failures: Int) -> TimeInterval {
        let index = max(0, failures - 1)
        return schedule[min(index, schedule.count - 1)]
    }
}
