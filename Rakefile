require "securerandom"
require "yaml"

desc "Builds packages"
task :build do
  version = ENV["VERSION"] || "0.0.0"
  docker_socket = ENV["DOCKER_SOCKET"] || "/var/run/docker.sock"
  sha = `git rev-parse --short HEAD`.chomp
  build = ENV["BUILD"] || "foss"
  buildid = SecureRandom.hex
  packages = (ENV["PACKAGES"] || "").split(",")
  packages = ["el7_64", "el8_64"] if packages.empty?
  builder = "registry.choria.io/choria/packager:el8-go1.20"
  source = "/go/src/github.com/choria-io/stream-replicator"

  packages.each do |pkg|
    if pkg =~ /^(.+?)_(.+)$/
       builder = "choria/packager:%s-go1.20" % $1
    end

    sh 'docker run --rm -v `pwd`:%s -e SOURCE_DIR=%s -e ARTIFACTS=%s -e SHA1="%s" -e BUILD="%s" -e VERSION="%s" -e PACKAGE=%s %s' % [
      source,
      source,
      source,
      sha,
      build,
      version,
      pkg,
      builder
    ]
  end
end
