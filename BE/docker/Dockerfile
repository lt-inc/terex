FROM python:3.11-slim
RUN apt-get update && apt-get install -y --no-install-recommends ffmpeg && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY docker/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
# Copy source — in local dev this is overridden by volume mount for hot reload
COPY . .
ENV PYTHONUNBUFFERED=1
ENTRYPOINT ["/entrypoint.sh"]
