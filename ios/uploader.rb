# frozen_string_literal: true

# uploader.rb
#
# Publishes release artifacts to the file_to_s3 service at files.chiq.me.
#
# Every upload here is *pinned* (`?name=`), meaning it lands at exactly that
# name and overwrites whatever was there. That is what gives the install page
# and the OTA manifest URLs that never move, so one Home Screen bookmark keeps
# installing the newest release forever.
#
# curl rather than an HTTP gem, so ./deploy needs no bundle.

require "cgi"
require "shellwords"
require "tempfile"

require_relative "ipa_builder"

module Uploader
  DEFAULT_BASE = "https://files.chiq.me"

  module_function

  def base_url
    ENV.fetch("AWH_UPLOAD_BASE", DEFAULT_BASE).sub(%r{/+\z}, "")
  end

  def token
    value = ENV["AWH_UPLOAD_TOKEN"]
    if value.nil? || value.strip.empty?
      IpaBuilder.die(
        "AWH_UPLOAD_TOKEN is not set. It must match file_to_s3's AUTH_TOKEN.\n" \
        "  export AWH_UPLOAD_TOKEN=..."
      )
    end
    value
  end

  # Uploads `path` under exactly `name`, returning its public URL.
  def upload(path, name:)
    IpaBuilder.die("file to upload not found: #{path}") unless File.file?(path)

    url = "#{base_url}/upload?name=#{CGI.escape(name)}"
    body, status = post(url, path)

    unless status == "200"
      IpaBuilder.die("upload of #{name} failed (HTTP #{status}): #{body}")
    end

    expected = "#{base_url}/files/#{name}"
    unless body == expected
      IpaBuilder.die("upload of #{name} returned #{body.inspect}, expected #{expected.inspect}. " \
                     "Does files.chiq.me have the ?name= pinning change deployed?")
    end

    body
  end

  # Returns [response_body, http_status]. The body goes to a temp file so the
  # status can be read from -w without the two getting interleaved.
  def post(url, path)
    Tempfile.create("awh-upload-response") do |out|
      status = IO.popen([
        "curl", "-sS",
        "-X", "POST", url,
        "-H", "Authorization: Bearer #{token}",
        "-F", "file=@#{path}",
        "-o", out.path,
        "-w", "%{http_code}"
      ], &:read)

      [File.read(out.path).strip, status.strip]
    end
  end
end
