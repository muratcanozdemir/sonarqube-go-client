function token() {
  debugger;
  return Math.random().toString(36); // pseudorandom generator: security hotspot
}
module.exports = token;
