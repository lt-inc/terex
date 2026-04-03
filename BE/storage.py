import os
import logging
from minio import Minio
from minio.error import S3Error

ENDPOINT  = os.getenv("S3_ENDPOINT")
ACCESS_KEY = os.getenv("S3_ACCESS_KEY")
SECRET_KEY = os.getenv("S3_SECRET_KEY")

BUCKET_BANNERS     = os.getenv("S3_BUCKET_BANNERS", "banners")
BUCKET_BACKGROUNDS = os.getenv("S3_BUCKET_BACKGROUNDS", "backgrounds")
BUCKET_COOKIES     = os.getenv("S3_BUCKET_COOKIES", "cookies")

client: Minio | None = None
if ENDPOINT and ACCESS_KEY and SECRET_KEY:
    client = Minio(ENDPOINT, access_key=ACCESS_KEY, secret_key=SECRET_KEY, secure=False)


def _ensure_bucket(bucket: str) -> None:
    if not client.bucket_exists(bucket):
        client.make_bucket(bucket)
        logging.info("Created bucket: %s", bucket)


def upload_stream(bucket: str, object_name: str, data, length: int, content_type: str = "application/octet-stream") -> None:
    """Upload a file-like object to MinIO."""
    _ensure_bucket(bucket)
    client.put_object(bucket, object_name, data, length, content_type=content_type)
    logging.info("Uploaded s3://%s/%s (%d bytes)", bucket, object_name, length)


def list_objects(bucket: str) -> list[str]:
    """Return all object names in a bucket."""
    try:
        _ensure_bucket(bucket)
        return [obj.object_name for obj in client.list_objects(bucket)]
    except S3Error as e:
        logging.error("MinIO list_objects error: %s", e)
        return []


def is_available() -> bool:
    return client is not None
