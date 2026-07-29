// TypeScript port of test262/harness/decimalToHexString.js. It defines
// decimalToHexString and decimalToPercentHexString, the two encoders the parse
// and encode tests include to spell a failing code point back as its four-digit
// hex. The port keeps the upstream algorithm and only names the number and string
// types the checker needs; the while (n) guard the sloppy truthiness stood for is
// written as the explicit not-equal it means for the unsigned value the shift
// leaves behind.
function decimalToHexString(n: number): string {
  var hex: string = "0123456789ABCDEF";
  n >>>= 0;
  var s: string = "";
  while (n !== 0) {
    s = hex[n & 0xf] + s;
    n >>>= 4;
  }
  while (s.length < 4) {
    s = "0" + s;
  }
  return s;
}

function decimalToPercentHexString(n: number): string {
  var hex: string = "0123456789ABCDEF";
  return "%" + hex[(n >> 4) & 0xf] + hex[n & 0xf];
}
