const path = require("path");
const { Transform } = require("readable-stream");

module.exports = {
  verifyConditions: (pluginConfig, context) => {
    const {
        scopes = [],              // Allow list
        excludeScopes = [],         // Block list
        filterOutMissingScope = false
      } = pluginConfig;

      const { logger } = context;

      Object.keys(require.cache)
        .filter((m) =>
          path
            .posix
            .normalize(m.replace(/\\/g, "/"))
            .endsWith("/node_modules/git-log-parser/src/index.js")
        )
        .forEach((moduleName) => {
          const originalParse = require.cache[moduleName].exports.parse;

          require.cache[moduleName].exports.parse = (config, options) => {
            const stream = originalParse(config, options);

            return stream.pipe(
              new Transform({
                objectMode: true,
                transform(chunk, enc, callback) {
                  const subject = chunk.subject || "";

                  // 🔥 0️⃣ Global Skip Rules (highest priority)
                  if (
                    subject.includes("[skip release]") ||
                    subject.includes("[skip ci]")
                  ) {
                    logger.log(
                      `[Filter] ⏭ Skipped: ${chunk.commit.short} (contains skip directive)`
                    );
                    return callback();
                  }

                  // Extract scope from Conventional Commit subject
                  const scopeMatch = subject.match(/^\w+\((.*?)\):/);
                  const currentScope = scopeMatch ? scopeMatch[1] : "";

                  const hasIncludeFilter = Array.isArray(scopes) && scopes.length > 0;
                  const hasExcludeFilter = Array.isArray(excludeScopes) && excludeScopes.length > 0;

                  // 🔥 If no filters at all → return early
                  if (!hasIncludeFilter && !hasExcludeFilter) {
                    this.push(chunk);
                    return callback();
                  }

                  // 1️⃣ Exclusion (always priority if defined)
                  if (hasExcludeFilter && excludeScopes.includes(currentScope)) {
                    logger.log(
                      `[Filter] ❌ Excluded: ${chunk.commit.short} (scope "${currentScope}")`
                    );
                    return callback();
                  }

                  // 2️⃣ Inclusion (only if defined)
                  if (hasIncludeFilter) {
                    const allowed = scopes.slice();

                    if (!filterOutMissingScope && !allowed.includes("")) {
                      allowed.push("");
                    }

                    if (!allowed.includes(currentScope)) {
                      logger.log(
                        `[Filter] ❌ Filtered: ${chunk.commit.short} (scope "${currentScope}")`
                      );
                      return callback();
                    }
                  }

                  this.push(chunk);
                  callback();
                },
              })
            );
          };
        });
  },
};