//! Renders HTML from stdin to a PNG on stdout.
//!
//! Usage: renderer --width W --height H [--css PATH] --font PATH [--font PATH ...]

use std::io::{Read, Write};

use anyhow::{Context, Result, bail};
use takumi::prelude::*;

fn main() -> Result<()> {
  let args = Args::parse(std::env::args().skip(1))?;

  let mut fonts = Fonts::default();
  for path in &args.fonts {
    let bytes = std::fs::read(path).with_context(|| format!("reading font {path}"))?;
    fonts
      .register(FontResource::new(bytes))
      .with_context(|| format!("registering font {path}"))?;
  }

  let mut html = String::new();
  std::io::stdin().read_to_string(&mut html).context("reading html from stdin")?;
  let node = takumi::from_html(&html, FromHtmlOptions::default()).context("parsing html")?;

  let stylesheet = match &args.css {
    Some(path) => {
      let css = std::fs::read_to_string(path).with_context(|| format!("reading css {path}"))?;
      StyleSheet::parse(&css).with_context(|| format!("parsing css {path}"))?
    }
    None => StyleSheet::default(),
  };

  let options = RenderOptions::builder()
    .viewport(Viewport::new((args.width, args.height)))
    .node(node)
    .fonts(&fonts)
    .stylesheet(stylesheet.into())
    .build();
  let image = takumi::render(options).context("rendering")?;

  let mut out = std::io::BufWriter::new(std::io::stdout().lock());
  takumi::write_image(&image, &mut out, OutputFormat::Png).context("encoding png")?;
  out.flush().context("writing png")?;
  Ok(())
}

struct Args {
  width: u32,
  height: u32,
  css: Option<String>,
  fonts: Vec<String>,
}

impl Args {
  fn parse(mut args: impl Iterator<Item = String>) -> Result<Self> {
    let (mut width, mut height, mut css, mut fonts) = (None, None, None, Vec::new());
    while let Some(flag) = args.next() {
      let value = args.next().with_context(|| format!("missing value for {flag}"))?;
      match flag.as_str() {
        "--width" => width = Some(value.parse().context("parsing --width")?),
        "--height" => height = Some(value.parse().context("parsing --height")?),
        "--css" => css = Some(value),
        "--font" => fonts.push(value),
        _ => bail!("unknown flag {flag}"),
      }
    }
    Ok(Self {
      width: width.context("--width is required")?,
      height: height.context("--height is required")?,
      css,
      fonts,
    })
  }
}
