# frozen_string_literal: true

# uploader.rb
#
# Publishes release artifacts into the files/ directory that the file_server
# service behind AWH_UPLOAD_BASE serves from.
#
# That service runs on the macmini, so a release is published by writing the
# artifact straight into the directory it serves, rather than POSTing it out to
# Cloudflare and back in again. Besides being pointless work for a large .ipa,
# the round trip is not reliable: request bodies past roughly 12 MB stall in the
# Cloudflare tunnel and the edge answers 524 before the origin ever sees them.
#
# Every publish is *pinned*: the artifact lands at exactly the name given and
# overwrites whatever was there. That is what gives the install page and the OTA
# manifest URLs that never move, so one Home Screen bookmark keeps installing the
# newest release forever.

require "fileutils"

require_relative "ipa_builder"

module Uploader
  # file_server is checked out beside this repo (~/c/file_server) and serves its
  # own files/ dir.
  DEFAULT_PUBLISH_DIR = File.expand_path("../file_server/files", IpaBuilder::REPO_ROOT)

  module_function

  def base_url
    base = IpaBuilder.env("UPLOAD_BASE").to_s
    if base.empty?
      IpaBuilder.die("set #{IpaBuilder::ENV_PREFIX}_UPLOAD_BASE (in .env) to the public URL of your file server")
    end
    base.sub(%r{/+\z}, "")
  end

  # Where the published artifacts land. Overridable so a service that lives
  # somewhere else does not need a code change here.
  def publish_dir
    IpaBuilder.env("PUBLISH_DIR") || DEFAULT_PUBLISH_DIR
  end

  # Dies unless the service's files/ directory is where we expect it. ../../deploy
  # calls this in preflight so a missing service fails before a build rather than
  # after one.
  def check_publish_dir!
    dir = publish_dir
    return dir if File.directory?(dir)

    IpaBuilder.die(
      "the file_server files/ directory was not found at #{dir}.\n" \
      "  Releases are published by writing into it, because the service serving\n" \
      "  #{base_url} runs on the macmini. Deploy from there, or set\n" \
      "  #{IpaBuilder::ENV_PREFIX}_PUBLISH_DIR if it moved."
    )
  end

  # Publishes `path` under exactly `name`, returning its public URL.
  def upload(path, name:)
    IpaBuilder.die("file to publish not found: #{path}") unless File.file?(path)
    IpaBuilder.die("unsafe publish name: #{name.inspect}") unless name == File.basename(name)

    target = File.join(check_publish_dir!, name)

    # Copy beside the target and rename, so a device downloading a pinned URL
    # never gets a half-written artifact while it is overwritten in place.
    staged = "#{target}.publishing-#{Process.pid}"
    begin
      FileUtils.cp(path, staged)
      File.rename(staged, target)
    ensure
      FileUtils.rm_f(staged)
    end

    "#{base_url}/files/#{name}"
  end
end
