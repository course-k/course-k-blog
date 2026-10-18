try {
  const response = await fetch('http://127.0.0.1:3000/healthz', {
    signal: AbortSignal.timeout(3000), redirect: 'manual',
  })
  if (response.status !== 200) process.exitCode = 1
  await response.body?.cancel()
} catch { process.exitCode = 1 }
