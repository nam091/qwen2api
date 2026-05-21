# Benchmark tốc độ qwen2api

## 1) Benchmark local (đã chạy)

Lệnh:

```powershell
powershell -ExecutionPolicy Bypass -File scripts/benchmark.ps1 -BaseUrl "http://127.0.0.1:15001" -ApiKey "sk-mykey" -Runs 3
```

Kết quả mẫu local hiện tại:
- avg: **2138.21ms**
- min: **89.61ms**
- max: **4129.12ms**

## 2) Benchmark trên VPS Singapore

Trên VPS (sau khi chạy qwen2api), dùng cùng script:

```powershell
powershell -ExecutionPolicy Bypass -File scripts/benchmark.ps1 -BaseUrl "http://127.0.0.1:15001" -ApiKey "sk-mykey" -Runs 10
```

Nếu test từ máy local vào VPS public endpoint:

```powershell
powershell -ExecutionPolicy Bypass -File scripts/benchmark.ps1 -BaseUrl "http://<VPS_IP>:15001" -ApiKey "sk-mykey" -Runs 10
```

## 3) So sánh với repo YuJunZhiXue/qwen2API

Deploy repo kia trên cùng VPS rồi chạy benchmark tương tự vào endpoint của họ.

Gợi ý so sánh công bằng:
- Cùng model
- Cùng prompt
- Cùng số runs
- Cùng token/account pool size

## 4) Kỳ vọng thực tế

Nếu VPS ở Singapore, thường sẽ nhanh hơn đáng kể so với chạy từ VN/local do RTT thấp hơn tới upstream Qwen.
- Streaming perceived speed: thường cải thiện rõ
- Non-stream latency: thường giảm 20-40% (tùy mạng)

## 5) Lưu ý

- Run đầu tiên thường chậm hơn (cold start/session init)
- Cần bỏ outlier hoặc tăng Runs (10-30) để số liệu ổn định
