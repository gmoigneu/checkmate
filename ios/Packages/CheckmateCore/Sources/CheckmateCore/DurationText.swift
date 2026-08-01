public enum DurationText {
  public static func minutes<T: BinaryInteger>(_ minutes: T) -> String {
    guard minutes >= 60 else { return "\(minutes)m" }
    let hours = minutes / 60
    let remainder = minutes % 60
    return remainder == 0 ? "\(hours)h" : "\(hours)h \(remainder)m"
  }
}
