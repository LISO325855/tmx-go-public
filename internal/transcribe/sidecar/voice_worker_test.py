"""Targeted regression test for voice_worker.device_and_compute()'s "auto" compute type.

No GPU and no ctranslate2 install needed: sys.modules is given a stand-in ctranslate2 so the
device/compute decision runs against known get_supported_compute_types() answers, including
Forge's actual blocker (a Pascal Quadro P4000, which does not list float16 as supported).

    python internal/transcribe/sidecar/voice_worker_test.py
"""
from __future__ import annotations

import importlib.util
import os
import sys
import unittest
from pathlib import Path
from unittest import mock

HERE = Path(__file__).resolve().parent


def _load_worker():
    spec = importlib.util.spec_from_file_location("voice_worker", HERE / "voice_worker.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class FakeCTranslate2:
    """Stands in for the ctranslate2 package: only the two calls device_and_compute() makes."""

    def __init__(self, cuda_devices: int, supported: set[str]) -> None:
        self._cuda_devices = cuda_devices
        self._supported = supported

    def get_cuda_device_count(self) -> int:
        return self._cuda_devices

    def get_supported_compute_types(self, device: str, device_index: int = 0) -> set[str]:
        return set(self._supported)


class DeviceAndComputeTest(unittest.TestCase):
    def setUp(self) -> None:
        self.worker = _load_worker()
        for k in ("TMX_VOICE_DEVICE", "TMX_VOICE_COMPUTE"):
            self.addCleanup(lambda k=k, v=os.environ.get(k): os.environ.pop(k, None) if v is None else os.environ.__setitem__(k, v))
            os.environ.pop(k, None)

    def _fake_ctranslate2(self, cuda_devices: int, supported: set[str]):
        return mock.patch.dict(sys.modules, {"ctranslate2": FakeCTranslate2(cuda_devices, supported)})

    def test_auto_on_pascal_gpu_without_fast_float16_picks_int8(self) -> None:
        # Forge's Quadro P4000 (compute capability 6.1): ctranslate2 does not list float16 among
        # its supported compute types there. The default config (device/compute_type both "auto")
        # must land on int8 by itself, with no manual compute_type override.
        with self._fake_ctranslate2(1, {"float32", "int8", "int8_float32"}):
            dev, comp = self.worker.device_and_compute()
        self.assertEqual((dev, comp), ("cuda", "int8"))

    def test_auto_on_a_gpu_with_float16_support_prefers_it(self) -> None:
        with self._fake_ctranslate2(1, {"float32", "float16", "int8_float16", "int8"}):
            dev, comp = self.worker.device_and_compute()
        self.assertEqual((dev, comp), ("cuda", "float16"))

    def test_auto_on_a_gpu_with_only_int8_float16_prefers_it_over_int8(self) -> None:
        with self._fake_ctranslate2(1, {"int8_float16", "int8", "float32"}):
            dev, comp = self.worker.device_and_compute()
        self.assertEqual((dev, comp), ("cuda", "int8_float16"))

    def test_auto_without_a_gpu_is_cpu_int8(self) -> None:
        with self._fake_ctranslate2(0, set()):
            dev, comp = self.worker.device_and_compute()
        self.assertEqual((dev, comp), ("cpu", "int8"))

    def test_auto_compute_type_with_an_unintrospectable_device_falls_back_to_int8(self) -> None:
        class BrokenCTranslate2(FakeCTranslate2):
            def get_supported_compute_types(self, device: str, device_index: int = 0) -> set[str]:
                raise RuntimeError("no such device")

        with mock.patch.dict(sys.modules, {"ctranslate2": BrokenCTranslate2(1, set())}):
            dev, comp = self.worker.device_and_compute()
        self.assertEqual((dev, comp), ("cuda", "int8"))

    def test_explicit_compute_type_overrides_auto_detection_on_cuda(self) -> None:
        # An explicit choice is never second-guessed against get_supported_compute_types, even
        # one that is not actually in the (fake) supported set.
        os.environ["TMX_VOICE_COMPUTE"] = "float32"
        with self._fake_ctranslate2(1, {"int8"}):
            dev, comp = self.worker.device_and_compute()
        self.assertEqual((dev, comp), ("cuda", "float32"))

    def test_explicit_device_with_auto_compute_still_probes_support(self) -> None:
        os.environ["TMX_VOICE_DEVICE"] = "cuda"
        with self._fake_ctranslate2(1, {"float32", "int8"}):
            dev, comp = self.worker.device_and_compute()
        self.assertEqual((dev, comp), ("cuda", "int8"))


if __name__ == "__main__":
    unittest.main()
