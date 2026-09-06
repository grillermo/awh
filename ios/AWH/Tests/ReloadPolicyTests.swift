import Foundation
import Testing
@testable import AWH

struct ReloadPolicyTests {
    @Test func rampsThroughTheSchedule() {
        #expect(ReloadPolicy.delay(afterFailures: 1) == 2)
        #expect(ReloadPolicy.delay(afterFailures: 2) == 5)
        #expect(ReloadPolicy.delay(afterFailures: 3) == 10)
    }

    @Test func plateausRatherThanGrowing() {
        #expect(ReloadPolicy.delay(afterFailures: 4) == 10)
        #expect(ReloadPolicy.delay(afterFailures: 99) == 10)
    }

    @Test func treatsZeroOrNegativeAsTheFirstRetry() {
        #expect(ReloadPolicy.delay(afterFailures: 0) == 2)
        #expect(ReloadPolicy.delay(afterFailures: -1) == 2)
    }
}
