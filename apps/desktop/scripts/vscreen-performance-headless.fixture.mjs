export async function installSyntheticSender(page) {
  await page.evaluate(() => {
      const { document, window, RTCPeerConnection } = globalThis;
      const canvas = document.createElement("canvas"); canvas.width = 640; canvas.height = 360; document.body.append(canvas);
      const drawing = canvas.getContext("2d"); let peer = null, timer = null, stream = null, frame = 0, tag = 1;
      const draw = () => {
        drawing.fillStyle = tag === 1 ? "#195ec5" : "#bc293e"; drawing.fillRect(0, 0, 640, 360);
        const bytes = new Uint8Array(20), view = new DataView(bytes.buffer);
        view.setUint16(0, 0x5653); view.setUint32(2, tag); view.setUint32(6, ++frame); view.setBigUint64(10, BigInt(Math.round((performance.timeOrigin + performance.now()) * 1e6)));
        let crc = 0xffff; for (const value of bytes.subarray(0, 18)) { crc ^= value << 8; for (let bit = 0; bit < 8; bit++) crc = (crc & 0x8000) ? ((crc << 1) ^ 0x1021) & 0xffff : (crc << 1) & 0xffff; } view.setUint16(18, crc);
        for (let bit = 0; bit < 160; bit++) { drawing.fillStyle = (bytes[Math.floor(bit / 8)] >> (7 - bit % 8)) & 1 ? "white" : "black"; drawing.fillRect(8 + (bit % 20) * 8, 8 + Math.floor(bit / 20) * 8, 8, 8); }
      };
      const close = () => { clearInterval(timer); peer?.close(); stream?.getTracks().forEach((track) => track.stop()); peer = null; };
      window.ownedSyntheticSender = {
        close,
        async offer(offer, source) {
          close(); tag = source === "source-a" ? 1 : 2; draw(); stream = canvas.captureStream(30); timer = setInterval(draw, 1000 / 30);
          peer = new RTCPeerConnection(); await peer.setRemoteDescription(offer); const track = stream.getVideoTracks()[0]; track.contentHint = "detail";
          const encoder = peer.addTrack(track, stream);
          await peer.setLocalDescription(await peer.createAnswer());
          const parameters = encoder.getParameters(); parameters.degradationPreference = "maintain-resolution";
          for (const encoding of parameters.encodings) Object.assign(encoding, { maxBitrate: 2_000_000, maxFramerate: 30, scaleResolutionDownBy: 1 });
          await encoder.setParameters(parameters);
          await new Promise((resolve, reject) => { if (peer.iceGatheringState === "complete") { resolve(); return; } const timeout = setTimeout(() => reject(new Error("owned ICE timeout")), 5000); peer.onicegatheringstatechange = () => { if (peer.iceGatheringState === "complete") { clearTimeout(timeout); resolve(); } }; });
          return peer.localDescription.toJSON();
        },
      };
    });
}
