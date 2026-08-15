import { INestApplication, ValidationPipe } from '@nestjs/common';
import { Test, TestingModule } from '@nestjs/testing';
import request from 'supertest';
import type { App } from 'supertest/types';
import { AppModule } from '../src/app.module';

describe('Videos (e2e)', () => {
  let app: INestApplication<App>;

  beforeAll(async () => {
    const moduleFixture: TestingModule = await Test.createTestingModule({
      imports: [AppModule],
    }).compile();

    app = moduleFixture.createNestApplication();
    app.useGlobalPipes(
      new ValidationPipe({ whitelist: true, transform: true }),
    );
    app.enableCors({
      origin: process.env.WEB_ORIGIN ?? 'http://localhost:3000',
    });
    await app.init();
  });

  afterAll(async () => {
    await app.close();
  });

  it('rejects a video with no title', async () => {
    await request(app.getHttpServer()).post('/videos').send({}).expect(400);
  });

  it('runs the full upload -> complete -> status flow against real infra', async () => {
    const createRes = await request(app.getHttpServer())
      .post('/videos')
      .send({ title: 'e2e test clip', description: 'created by jest' })
      .expect(201);

    const { id, uploadUrl } = createRes.body as {
      id: string;
      uploadUrl: string;
    };
    expect(id).toBeDefined();
    expect(uploadUrl).toContain('X-Amz-Signature');

    // Real PUT against local MinIO through the presigned URL.
    const putRes = await fetch(uploadUrl, {
      method: 'PUT',
      body: 'fake source bytes',
    });
    expect(putRes.status).toBe(200);

    const completeRes = await request(app.getHttpServer())
      .post(`/videos/${id}/complete`)
      .expect(201);
    expect((completeRes.body as { status: string }).status).toBe('queued');

    // Completing twice must not silently re-enqueue.
    await request(app.getHttpServer())
      .post(`/videos/${id}/complete`)
      .expect(409);

    const statusRes = await request(app.getHttpServer())
      .get(`/videos/${id}`)
      .expect(200);
    expect(statusRes.body).toMatchObject({
      id,
      status: 'queued',
      renditions: [],
      masterManifestUrl: null,
      thumbnailUrl: null,
    });
  });

  it('404s for an unknown video id', async () => {
    await request(app.getHttpServer())
      .get('/videos/00000000-0000-0000-0000-000000000000')
      .expect(404);
  });

  it('allows the configured web origin via CORS preflight', async () => {
    const res = await request(app.getHttpServer())
      .options('/videos')
      .set('Origin', 'http://localhost:3000')
      .set('Access-Control-Request-Method', 'GET')
      .expect(204);

    expect(res.headers['access-control-allow-origin']).toBe(
      'http://localhost:3000',
    );
  });
});
