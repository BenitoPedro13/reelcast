import { Global, Injectable, Module, OnModuleDestroy } from '@nestjs/common';
import { ConfigService } from '@nestjs/config';
import { drizzle, NodePgDatabase } from 'drizzle-orm/node-postgres';
import { Pool } from 'pg';
import * as schema from './schema';

export const DRIZZLE = Symbol('DRIZZLE');
export type DrizzleDb = NodePgDatabase<typeof schema>;

@Injectable()
class PgPool extends Pool implements OnModuleDestroy {
  constructor(config: ConfigService) {
    super({ connectionString: config.getOrThrow<string>('DATABASE_URL') });
  }

  async onModuleDestroy(): Promise<void> {
    await this.end();
  }
}

@Global()
@Module({
  providers: [
    PgPool,
    {
      provide: DRIZZLE,
      inject: [PgPool],
      useFactory: (pool: PgPool): DrizzleDb => drizzle(pool, { schema }),
    },
  ],
  exports: [DRIZZLE],
})
export class DrizzleModule {}
